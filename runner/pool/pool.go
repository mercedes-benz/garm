// Copyright 2022 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package pool

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudbase/garm/auth"
	dbCommon "github.com/cloudbase/garm/database/common"
	runnerErrors "github.com/cloudbase/garm/errors"
	"github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/runner/common"
	providerCommon "github.com/cloudbase/garm/runner/providers/common"
	"github.com/cloudbase/garm/util"

	"github.com/google/go-github/v48/github"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

var (
	poolIDLabelprefix     = "runner-pool-id:"
	controllerLabelPrefix = "runner-controller-id:"
)

const (
	// maxCreateAttempts is the number of times we will attempt to create an instance
	// before we give up.
	// TODO: make this configurable(?)
	maxCreateAttempts = 5
)

type basePoolManager struct {
	ctx          context.Context
	controllerID string

	store dbCommon.Store

	providers map[string]common.Provider
	tools     []*github.RunnerApplicationDownload
	quit      chan struct{}
	done      chan struct{}

	helper       poolHelper
	credsDetails params.GithubCredentials

	managerIsRunning   bool
	managerErrorReason string

	runnerMux sync.Mutex

	mux sync.Mutex
}

func (r *basePoolManager) HandleWorkflowJob(job params.WorkflowJob) error {
	if err := r.helper.ValidateOwner(job); err != nil {
		return errors.Wrap(err, "validating owner")
	}

	var jobParams params.Job
	var err error
	defer func() {
		// we're updating the job in the database, regardless of whether it was successful or not.
		// or if it was meant for this pool or not. Github will send the same job data to all hierarchies
		// that have been configured to work with garm. Updating the job at all levels should yield the same
		// outcome in the db.
		if jobParams.ID != 0 {
			if _, jobErr := r.store.CreateOrUpdateJob(r.ctx, jobParams); jobErr != nil {
				log.Printf("failed to update job %d: %s", jobParams.ID, jobErr)
			}
		}
	}()

	switch job.Action {
	case "queued":
		// Record the job in the database. Queued jobs will be picked up by the consumeQueuedJobs() method
		// when reconciling.
		jobParams, err = r.paramsWorkflowJobToParamsJob(job)
		if err != nil {
			return errors.Wrap(err, "converting job to params")
		}
	case "completed":
		jobParams, err = r.paramsWorkflowJobToParamsJob(job)
		if err != nil {
			if errors.Is(err, runnerErrors.ErrNotFound) {
				// Unassigned jobs will have an empty runner_name.
				// We also need to ignore not found errors, as we may get a webhook regarding
				// a workflow that is handled by a runner at a different hierarchy level.
				return nil
			}
			return errors.Wrap(err, "converting job to params")
		}

		// update instance workload state.
		if _, err := r.setInstanceRunnerStatus(jobParams.RunnerName, providerCommon.RunnerTerminated); err != nil {
			if errors.Is(err, runnerErrors.ErrNotFound) {
				return nil
			}
			log.Printf("failed to update runner %s status", util.SanitizeLogEntry(jobParams.RunnerName))
			return errors.Wrap(err, "updating runner")
		}
		log.Printf("marking instance %s as pending_delete", util.SanitizeLogEntry(jobParams.RunnerName))
		if _, err := r.setInstanceStatus(jobParams.RunnerName, providerCommon.InstancePendingDelete, nil); err != nil {
			if errors.Is(err, runnerErrors.ErrNotFound) {
				return nil
			}
			log.Printf("failed to update runner %s status", util.SanitizeLogEntry(jobParams.RunnerName))
			return errors.Wrap(err, "updating runner")
		}
	case "in_progress":
		jobParams, err = r.paramsWorkflowJobToParamsJob(job)
		if err != nil {
			if errors.Is(err, runnerErrors.ErrNotFound) {
				// This is most likely a runner we're not managing. If we define a repo from within an org
				// and also define that same org, we will get a hook from github from both the repo and the org
				// regarding the same workflow. We look for the runner in the database, and make sure it exists and is
				// part of a pool that this manager is responsible for. A not found error here will most likely mean
				// that we are not responsible for that runner, and we should ignore it.
				return nil
			}
			return errors.Wrap(err, "converting job to params")
		}

		// update instance workload state.
		instance, err := r.setInstanceRunnerStatus(jobParams.RunnerName, providerCommon.RunnerActive)
		if err != nil {
			if errors.Is(err, runnerErrors.ErrNotFound) {
				return nil
			}
			log.Printf("failed to update runner %s status", util.SanitizeLogEntry(jobParams.RunnerName))
			return errors.Wrap(err, "updating runner")
		}

		// A runner has picked up the job, and is now running it. It may need to be replaced if the pool has
		// a minimum number of idle runners configured.
		pool, err := r.store.GetPoolByID(r.ctx, instance.PoolID)
		if err != nil {
			return errors.Wrap(err, "getting pool")
		}
		if err := r.ensureIdleRunnersForOnePool(pool); err != nil {
			log.Printf("error ensuring idle runners for pool %s: %s", pool.ID, err)
		}
	}
	return nil
}

func (r *basePoolManager) loop() {
	scaleDownTimer := time.NewTicker(common.PoolScaleDownInterval)
	consolidateTimer := time.NewTicker(common.PoolConsilitationInterval)
	reapTimer := time.NewTicker(common.PoolReapTimeoutInterval)
	toolUpdateTimer := time.NewTicker(common.PoolToolUpdateInterval)
	defer func() {
		log.Printf("%s loop exited", r.helper.String())
		scaleDownTimer.Stop()
		consolidateTimer.Stop()
		reapTimer.Stop()
		toolUpdateTimer.Stop()
		close(r.done)
	}()
	log.Printf("starting loop for %s", r.helper.String())

	// Consolidate runners on loop start. Provider runners must match runners
	// in github and DB. When a Workflow job is received, we will first create/update
	// an entity in the database, before sending the request to the provider to create/delete
	// an instance. If a "queued" job is received, we create an entity in the db with
	// a state of "pending_create". Once that instance is up and calls home, it is marked
	// as "active". If a "completed" job is received from github, we mark the instance
	// as "pending_delete". Once the provider deletes the instance, we mark it as "deleted"
	// in the database.
	// We also ensure we have runners created based on pool characteristics. This is where
	// we spin up "MinWorkers" for each runner type.
	for {
		switch r.managerIsRunning {
		case true:
			select {
			case <-reapTimer.C:
				runners, err := r.helper.GetGithubRunners()
				if err != nil {
					failureReason := fmt.Sprintf("error fetching github runners for %s: %s", r.helper.String(), err)
					r.setPoolRunningState(false, failureReason)
					log.Print(failureReason)
					if errors.Is(err, runnerErrors.ErrUnauthorized) {
						break
					}
					continue
				}
				if err := r.reapTimedOutRunners(runners); err != nil {
					log.Printf("failed to reap timed out runners: %q", err)
				}

				if err := r.runnerCleanup(); err != nil {
					failureReason := fmt.Sprintf("failed to clean runners for %s: %q", r.helper.String(), err)
					log.Print(failureReason)
					if errors.Is(err, runnerErrors.ErrUnauthorized) {
						r.setPoolRunningState(false, failureReason)
					}
				}
			case <-consolidateTimer.C:
				// consolidate.
				if err := r.consumeQueuedJobs(r.ctx); err != nil {
					log.Printf("failed to consume queued jobs: %q", err)
				}
				r.consolidate()
			case <-scaleDownTimer.C:
				r.scaleDown()
			case <-toolUpdateTimer.C:
				// Update tools cache.
				tools, err := r.helper.FetchTools()
				if err != nil {
					failureReason := fmt.Sprintf("failed to update tools for repo %s: %s", r.helper.String(), err)
					r.setPoolRunningState(false, failureReason)
					log.Print(failureReason)
					if errors.Is(err, runnerErrors.ErrUnauthorized) {
						break
					}
					continue
				}
				r.mux.Lock()
				r.tools = tools
				r.mux.Unlock()
			case <-r.ctx.Done():
				// daemon is shutting down.
				return
			case <-r.quit:
				// this worker was stopped.
				return
			}
		default:
			select {
			case <-r.ctx.Done():
				// daemon is shutting down.
				return
			case <-r.quit:
				// this worker was stopped.
				return
			default:
				log.Printf("attempting to start pool manager for %s", r.helper.String())
				tools, err := r.helper.FetchTools()
				var failureReason string
				if err != nil {
					failureReason = fmt.Sprintf("failed to fetch tools from github for %s: %q", r.helper.String(), err)
					r.setPoolRunningState(false, failureReason)
					log.Print(failureReason)
					if errors.Is(err, runnerErrors.ErrUnauthorized) {
						r.waitForTimeoutOrCanceled(common.UnauthorizedBackoffTimer)
					} else {
						r.waitForTimeoutOrCanceled(60 * time.Second)
					}
					continue
				}
				r.mux.Lock()
				r.tools = tools
				r.mux.Unlock()

				if err := r.runnerCleanup(); err != nil {
					failureReason = fmt.Sprintf("failed to clean runners for %s: %q", r.helper.String(), err)
					log.Print(failureReason)
					if errors.Is(err, runnerErrors.ErrUnauthorized) {
						r.setPoolRunningState(false, failureReason)
						r.waitForTimeoutOrCanceled(common.UnauthorizedBackoffTimer)
					}
					continue
				}
				r.setPoolRunningState(true, "")
			}
		}
	}
}

func controllerIDFromLabels(labels []string) string {
	for _, lbl := range labels {
		if strings.HasPrefix(lbl, controllerLabelPrefix) {
			return lbl[len(controllerLabelPrefix):]
		}
	}
	return ""
}

func labelsFromRunner(runner *github.Runner) []string {
	if runner == nil || runner.Labels == nil {
		return []string{}
	}

	var labels []string
	for _, val := range runner.Labels {
		if val == nil {
			continue
		}
		labels = append(labels, val.GetName())
	}
	return labels
}

// isManagedRunner returns true if labels indicate the runner belongs to a pool
// this manager is responsible for.
func (r *basePoolManager) isManagedRunner(labels []string) bool {
	runnerControllerID := controllerIDFromLabels(labels)
	return runnerControllerID == r.controllerID
}

// cleanupOrphanedProviderRunners compares runners in github with local runners and removes
// any local runners that are not present in Github. Runners that are "idle" in our
// provider, but do not exist in github, will be removed. This can happen if the
// garm was offline while a job was executed by a github action. When this
// happens, github will remove the ephemeral worker and send a webhook our way.
// If we were offline and did not process the webhook, the instance will linger.
// We need to remove it from the provider and database.
func (r *basePoolManager) cleanupOrphanedProviderRunners(runners []*github.Runner) error {
	dbInstances, err := r.helper.FetchDbInstances()
	if err != nil {
		return errors.Wrap(err, "fetching instances from db")
	}

	runnerNames := map[string]bool{}
	for _, run := range runners {
		if !r.isManagedRunner(labelsFromRunner(run)) {
			log.Printf("runner %s is not managed by a pool belonging to %s", *run.Name, r.helper.String())
			continue
		}
		runnerNames[*run.Name] = true
	}

	for _, instance := range dbInstances {
		switch providerCommon.InstanceStatus(instance.Status) {
		case providerCommon.InstancePendingCreate,
			providerCommon.InstancePendingDelete:
			// this instance is in the process of being created or is awaiting deletion.
			// Instances in pending_create did not get a chance to register themselves in,
			// github so we let them be for now.
			continue
		}

		switch instance.RunnerStatus {
		case providerCommon.RunnerPending, providerCommon.RunnerInstalling:
			// runner is still installing. We give it a chance to finish.
			log.Printf("runner %s is still installing, give it a chance to finish", instance.Name)
			continue
		}

		if time.Since(instance.UpdatedAt).Minutes() < 5 {
			// instance was updated recently. We give it a chance to register itself in github.
			log.Printf("instance %s was updated recently, skipping check", instance.Name)
			continue
		}

		if ok := runnerNames[instance.Name]; !ok {
			// Set pending_delete on DB field. Allow consolidate() to remove it.
			if _, err := r.setInstanceStatus(instance.Name, providerCommon.InstancePendingDelete, nil); err != nil {
				log.Printf("failed to update runner %s status", instance.Name)
				return errors.Wrap(err, "updating runner")
			}
		}
	}
	return nil
}

// reapTimedOutRunners will mark as pending_delete any runner that has a status
// of "running" in the provider, but that has not registered with Github, and has
// received no new updates in the configured timeout interval.
func (r *basePoolManager) reapTimedOutRunners(runners []*github.Runner) error {
	dbInstances, err := r.helper.FetchDbInstances()
	if err != nil {
		return errors.Wrap(err, "fetching instances from db")
	}

	runnersByName := map[string]*github.Runner{}
	for _, run := range runners {
		if !r.isManagedRunner(labelsFromRunner(run)) {
			log.Printf("runner %s is not managed by a pool belonging to %s", *run.Name, r.helper.String())
			continue
		}
		runnersByName[*run.Name] = run
	}

	for _, instance := range dbInstances {
		pool, err := r.store.GetPoolByID(r.ctx, instance.PoolID)
		if err != nil {
			return errors.Wrap(err, "fetching instance pool info")
		}
		if time.Since(instance.UpdatedAt).Minutes() < float64(pool.RunnerTimeout()) {
			continue
		}

		// There are 2 cases (currently) where we consider a runner as timed out:
		//   * The runner never joined github within the pool timeout
		//   * The runner managed to join github, but the setup process failed later and the runner
		//     never started on the instance.
		//
		// There are several steps in the user data that sets up the runner:
		//   * Download and unarchive the runner from github (or used the cached version)
		//   * Configure runner (connects to github). At this point the runner is seen as offline.
		//   * Install the service
		//   * Set SELinux context (if SELinux is enabled)
		//   * Start the service (if successful, the runner will transition to "online")
		//   * Get the runner ID
		//
		// If we fail getting the runner ID after it's started, garm will set the runner status to "failed",
		// even though, technically the runner is online and fully functional. This is why we check here for
		// both the runner status as reported by GitHub and the runner status as reported by the provider.
		// If the runner is "offline" and marked as "failed", it should be safe to reap it.
		if runner, ok := runnersByName[instance.Name]; !ok || (runner.GetStatus() == "offline" && instance.RunnerStatus == providerCommon.RunnerFailed) {
			log.Printf("reaping timed-out/failed runner %s", instance.Name)
			if err := r.ForceDeleteRunner(instance); err != nil {
				log.Printf("failed to update runner %s status", instance.Name)
				return errors.Wrap(err, "updating runner")
			}
		}
	}
	return nil
}

func instanceInList(instanceName string, instances []params.Instance) (params.Instance, bool) {
	for _, val := range instances {
		if val.Name == instanceName {
			return val, true
		}
	}
	return params.Instance{}, false
}

// cleanupOrphanedGithubRunners will forcefully remove any github runners that appear
// as offline and for which we no longer have a local instance.
// This may happen if someone manually deletes the instance in the provider. We need to
// first remove the instance from github, and then from our database.
func (r *basePoolManager) cleanupOrphanedGithubRunners(runners []*github.Runner) error {
	poolInstanceCache := map[string][]params.Instance{}
	g, ctx := errgroup.WithContext(r.ctx)
	for _, runner := range runners {
		if !r.isManagedRunner(labelsFromRunner(runner)) {
			log.Printf("runner %s is not managed by a pool belonging to %s", *runner.Name, r.helper.String())
			continue
		}

		status := runner.GetStatus()
		if status != "offline" {
			// Runner is online. Ignore it.
			continue
		}

		dbInstance, err := r.store.GetInstanceByName(r.ctx, *runner.Name)
		if err != nil {
			if !errors.Is(err, runnerErrors.ErrNotFound) {
				return errors.Wrap(err, "fetching instance from DB")
			}
			// We no longer have a DB entry for this instance, and the runner appears offline in github.
			// Previous forceful removal may have failed?
			log.Printf("Runner %s has no database entry in garm, removing from github", *runner.Name)
			resp, err := r.helper.RemoveGithubRunner(*runner.ID)
			if err != nil {
				// Removed in the meantime?
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					continue
				}
				return errors.Wrap(err, "removing runner")
			}
			continue
		}

		switch providerCommon.InstanceStatus(dbInstance.Status) {
		case providerCommon.InstancePendingDelete, providerCommon.InstanceDeleting:
			// already marked for deletion or is in the process of being deleted.
			// Let consolidate take care of it.
			continue
		}

		pool, err := r.helper.GetPoolByID(dbInstance.PoolID)
		if err != nil {
			return errors.Wrap(err, "fetching pool")
		}

		// check if the provider still has the instance.
		provider, ok := r.providers[pool.ProviderName]
		if !ok {
			return fmt.Errorf("unknown provider %s for pool %s", pool.ProviderName, pool.ID)
		}

		var poolInstances []params.Instance
		poolInstances, ok = poolInstanceCache[pool.ID]
		if !ok {
			log.Printf("updating instances cache for pool %s", pool.ID)
			poolInstances, err = provider.ListInstances(r.ctx, pool.ID)
			if err != nil {
				return errors.Wrapf(err, "fetching instances for pool %s", pool.ID)
			}
			poolInstanceCache[pool.ID] = poolInstances
		}
		// See: https://golang.org/doc/faq#closures_and_goroutines
		runner := runner
		g.Go(func() error {
			providerInstance, ok := instanceInList(dbInstance.Name, poolInstances)
			if !ok {
				// The runner instance is no longer on the provider, and it appears offline in github.
				// It should be safe to force remove it.
				log.Printf("Runner instance for %s is no longer on the provider, removing from github", dbInstance.Name)
				resp, err := r.helper.RemoveGithubRunner(*runner.ID)
				if err != nil {
					// Removed in the meantime?
					if resp != nil && resp.StatusCode == http.StatusNotFound {
						log.Printf("runner dissapeared from github")
					} else {
						return errors.Wrap(err, "removing runner from github")
					}
				}
				// Remove the database entry for the runner.
				log.Printf("Removing %s from database", dbInstance.Name)
				if err := r.store.DeleteInstance(ctx, dbInstance.PoolID, dbInstance.Name); err != nil {
					return errors.Wrap(err, "removing runner from database")
				}
				return nil
			}

			if providerInstance.Status == providerCommon.InstanceRunning {
				// instance is running, but github reports runner as offline. Log the event.
				// This scenario may require manual intervention.
				// Perhaps it just came online and github did not yet change it's status?
				log.Printf("instance %s is online but github reports runner as offline", dbInstance.Name)
				return nil
			} else {
				log.Printf("instance %s was found in stopped state; starting", dbInstance.Name)
				//start the instance
				if err := provider.Start(r.ctx, dbInstance.ProviderID); err != nil {
					return errors.Wrapf(err, "starting instance %s", dbInstance.ProviderID)
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return errors.Wrap(err, "removing orphaned github runners")
	}
	return nil
}

func (r *basePoolManager) fetchInstance(runnerName string) (params.Instance, error) {
	runner, err := r.store.GetInstanceByName(r.ctx, runnerName)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "fetching instance")
	}

	_, err = r.helper.GetPoolByID(runner.PoolID)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "fetching pool")
	}

	return runner, nil
}

func (r *basePoolManager) setInstanceRunnerStatus(runnerName string, status providerCommon.RunnerStatus) (params.Instance, error) {
	updateParams := params.UpdateInstanceParams{
		RunnerStatus: status,
	}

	instance, err := r.updateInstance(runnerName, updateParams)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "updating runner state")
	}
	return instance, nil
}

func (r *basePoolManager) updateInstance(runnerName string, update params.UpdateInstanceParams) (params.Instance, error) {
	runner, err := r.fetchInstance(runnerName)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "fetching instance")
	}

	instance, err := r.store.UpdateInstance(r.ctx, runner.ID, update)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "updating runner state")
	}
	return instance, nil
}

func (r *basePoolManager) setInstanceStatus(runnerName string, status providerCommon.InstanceStatus, providerFault []byte) (params.Instance, error) {
	updateParams := params.UpdateInstanceParams{
		Status:        status,
		ProviderFault: providerFault,
	}

	instance, err := r.updateInstance(runnerName, updateParams)
	if err != nil {
		return params.Instance{}, errors.Wrap(err, "updating runner state")
	}
	return instance, nil
}

func (r *basePoolManager) AddRunner(ctx context.Context, poolID string) error {
	pool, err := r.helper.GetPoolByID(poolID)
	if err != nil {
		return errors.Wrap(err, "fetching pool")
	}

	name := fmt.Sprintf("%s-%s", pool.GetRunnerPrefix(), util.NewID())

	createParams := params.CreateInstanceParams{
		Name:              name,
		Status:            providerCommon.InstancePendingCreate,
		RunnerStatus:      providerCommon.RunnerPending,
		OSArch:            pool.OSArch,
		OSType:            pool.OSType,
		CallbackURL:       r.helper.GetCallbackURL(),
		MetadataURL:       r.helper.GetMetadataURL(),
		CreateAttempt:     1,
		GitHubRunnerGroup: pool.GitHubRunnerGroup,
	}

	_, err = r.store.CreateInstance(r.ctx, poolID, createParams)
	if err != nil {
		return errors.Wrap(err, "creating instance")
	}

	return nil
}

func (r *basePoolManager) Status() params.PoolManagerStatus {
	r.mux.Lock()
	defer r.mux.Unlock()
	return params.PoolManagerStatus{
		IsRunning:     r.managerIsRunning,
		FailureReason: r.managerErrorReason,
	}
}

func (r *basePoolManager) waitForTimeoutOrCanceled(timeout time.Duration) {
	log.Printf("sleeping for %.2f minutes", timeout.Minutes())
	select {
	case <-time.After(timeout):
	case <-r.ctx.Done():
	case <-r.quit:
	}
}

func (r *basePoolManager) setPoolRunningState(isRunning bool, failureReason string) {
	r.mux.Lock()
	r.managerErrorReason = failureReason
	r.managerIsRunning = isRunning
	r.mux.Unlock()
}

func (r *basePoolManager) addInstanceToProvider(instance params.Instance) error {
	pool, err := r.helper.GetPoolByID(instance.PoolID)
	if err != nil {
		return errors.Wrap(err, "fetching pool")
	}

	provider, ok := r.providers[pool.ProviderName]
	if !ok {
		return fmt.Errorf("unknown provider %s for pool %s", pool.ProviderName, pool.ID)
	}

	labels := []string{}
	for _, tag := range pool.Tags {
		labels = append(labels, tag.Name)
	}
	labels = append(labels, r.controllerLabel())
	labels = append(labels, r.poolLabel(pool.ID))

	jwtValidity := pool.RunnerTimeout()

	entity := r.helper.String()
	jwtToken, err := auth.NewInstanceJWTToken(instance, r.helper.JwtToken(), entity, pool.PoolType(), jwtValidity)
	if err != nil {
		return errors.Wrap(err, "fetching instance jwt token")
	}

	bootstrapArgs := params.BootstrapInstance{
		Name:              instance.Name,
		Tools:             r.tools,
		RepoURL:           r.helper.GithubURL(),
		MetadataURL:       instance.MetadataURL,
		CallbackURL:       instance.CallbackURL,
		InstanceToken:     jwtToken,
		OSArch:            pool.OSArch,
		OSType:            pool.OSType,
		Flavor:            pool.Flavor,
		Image:             pool.Image,
		ExtraSpecs:        pool.ExtraSpecs,
		Labels:            labels,
		PoolID:            instance.PoolID,
		CACertBundle:      r.credsDetails.CABundle,
		GitHubRunnerGroup: instance.GitHubRunnerGroup,
	}

	var instanceIDToDelete string

	defer func() {
		if instanceIDToDelete != "" {
			if err := provider.DeleteInstance(r.ctx, instanceIDToDelete); err != nil {
				if !errors.Is(err, runnerErrors.ErrNotFound) {
					log.Printf("failed to cleanup instance: %s", instanceIDToDelete)
				}
			}
		}
	}()

	providerInstance, err := provider.CreateInstance(r.ctx, bootstrapArgs)
	if err != nil {
		instanceIDToDelete = instance.Name
		return errors.Wrap(err, "creating instance")
	}

	if providerInstance.Status == providerCommon.InstanceError {
		instanceIDToDelete = instance.ProviderID
		if instanceIDToDelete == "" {
			instanceIDToDelete = instance.Name
		}
	}

	updateInstanceArgs := r.updateArgsFromProviderInstance(providerInstance)
	if _, err := r.store.UpdateInstance(r.ctx, instance.ID, updateInstanceArgs); err != nil {
		return errors.Wrap(err, "updating instance")
	}
	return nil
}

func (r *basePoolManager) getRunnerDetailsFromJob(job params.WorkflowJob) (params.RunnerInfo, error) {
	runnerInfo := params.RunnerInfo{
		Name:   job.WorkflowJob.RunnerName,
		Labels: job.WorkflowJob.Labels,
	}

	var err error
	if job.WorkflowJob.RunnerName == "" {
		// Runner name was not set in WorkflowJob by github. We can still attempt to
		// fetch the info we need, using the workflow run ID, from the API.
		log.Printf("runner name not found in workflow job, attempting to fetch from API")
		runnerInfo, err = r.helper.GetRunnerInfoFromWorkflow(job)
		if err != nil {
			return params.RunnerInfo{}, errors.Wrap(err, "fetching runner name from API")
		}
	}

	runnerDetails, err := r.store.GetInstanceByName(context.Background(), runnerInfo.Name)
	if err != nil {
		log.Printf("could not find runner details for %s", util.SanitizeLogEntry(runnerInfo.Name))
		return params.RunnerInfo{}, errors.Wrap(err, "fetching runner details")
	}

	if _, err := r.helper.GetPoolByID(runnerDetails.PoolID); err != nil {
		log.Printf("runner %s (pool ID: %s) does not belong to any pool we manage: %s", runnerDetails.Name, runnerDetails.PoolID, err)
		return params.RunnerInfo{}, errors.Wrap(err, "fetching pool for instance")
	}
	return runnerInfo, nil
}

// paramsWorkflowJobToParamsJob returns a params.Job from a params.WorkflowJob, and aditionally determines
// if the runner belongs to this pool or not. It will always return a valid params.Job, even if it errs out.
// This allows us to still update the job in the database, even if we determined that it wasn't necessarily meant
// for this pool.
// If garm manages multiple hierarchies (repos, org, enterprise) which involve the same repo, we will get a hook
// whenever a job involving our repo triggers a hook. So even if the job is picked up by a runner at the enterprise
// level, the repo and org still get a hook.
// We even get a hook if a particular job is picked up by a GitHub hosted runner. We don't know who will pick up the job
// until the "in_progress" event is sent and we can see which runner picked it up.
//
// We save the details of that job at every level, because we want to at least update the status of the job. We make
// decissions based on the status of saved jobs. A "queued" job will prompt garm to search for an appropriate pool
// and spin up a runner there if no other idle runner exists to pick it up.
func (r *basePoolManager) paramsWorkflowJobToParamsJob(job params.WorkflowJob) (params.Job, error) {
	jobParams := params.Job{
		ID:              job.WorkflowJob.ID,
		Action:          job.Action,
		RunID:           job.WorkflowJob.RunID,
		Status:          job.WorkflowJob.Status,
		Conclusion:      job.WorkflowJob.Conclusion,
		StartedAt:       job.WorkflowJob.StartedAt,
		CompletedAt:     job.WorkflowJob.CompletedAt,
		Name:            job.WorkflowJob.Name,
		GithubRunnerID:  job.WorkflowJob.RunnerID,
		RunnerGroupID:   job.WorkflowJob.RunnerGroupID,
		RunnerGroupName: job.WorkflowJob.RunnerGroupName,
		RepositoryName:  job.Repository.Name,
		RepositoryOwner: job.Repository.Owner.Login,
		Labels:          job.WorkflowJob.Labels,
	}

	runnerName := job.WorkflowJob.RunnerName
	if job.Action != "queued" && runnerName == "" {
		// Runner name was not set in WorkflowJob by github. We can still attempt to fetch the info we need,
		// using the workflow run ID, from the API.
		// We may still get no runner name. In situations such as jobs being cancelled before a runner had the chance
		// to pick up the job, the runner name is not available from the API.
		runnerInfo, err := r.getRunnerDetailsFromJob(job)
		if err != nil && !errors.Is(err, runnerErrors.ErrNotFound) {
			return jobParams, errors.Wrap(err, "fetching runner details")
		}
		runnerName = runnerInfo.Name
	}

	jobParams.RunnerName = runnerName

	asUUID, err := uuid.Parse(r.ID())
	if err != nil {
		return jobParams, errors.Wrap(err, "parsing pool ID as UUID")
	}

	switch r.helper.PoolType() {
	case params.EnterprisePool:
		jobParams.EnterpriseID = asUUID
	case params.RepositoryPool:
		jobParams.RepoID = asUUID
	case params.OrganizationPool:
		jobParams.OrgID = asUUID
	default:
		return jobParams, errors.Errorf("unknown pool type: %s", r.helper.PoolType())
	}

	return jobParams, nil
}

func (r *basePoolManager) poolLabel(poolID string) string {
	return fmt.Sprintf("%s%s", poolIDLabelprefix, poolID)
}

func (r *basePoolManager) controllerLabel() string {
	return fmt.Sprintf("%s%s", controllerLabelPrefix, r.controllerID)
}

func (r *basePoolManager) updateArgsFromProviderInstance(providerInstance params.Instance) params.UpdateInstanceParams {
	return params.UpdateInstanceParams{
		ProviderID:    providerInstance.ProviderID,
		OSName:        providerInstance.OSName,
		OSVersion:     providerInstance.OSVersion,
		Addresses:     providerInstance.Addresses,
		Status:        providerInstance.Status,
		RunnerStatus:  providerInstance.RunnerStatus,
		ProviderFault: providerInstance.ProviderFault,
	}
}
func (r *basePoolManager) scaleDownOnePool(pool params.Pool) {
	if !pool.Enabled {
		return
	}

	existingInstances, err := r.store.ListPoolInstances(r.ctx, pool.ID)
	if err != nil {
		log.Printf("failed to ensure minimum idle workers for pool %s: %s", pool.ID, err)
		return
	}

	idleWorkers := []params.Instance{}
	for _, inst := range existingInstances {
		// Idle runners that have been spawned and are still idle after 5 minutes, are take into
		// consideration for scale-down. The 5 minute grace period prevents a situation where a
		// "queued" workflow triggers the creation of a new idle runner, and this routine reaps
		// an idle runner before they have a chance to pick up a job.
		if providerCommon.RunnerStatus(inst.RunnerStatus) == providerCommon.RunnerIdle &&
			providerCommon.InstanceStatus(inst.Status) == providerCommon.InstanceRunning &&
			time.Since(inst.UpdatedAt).Minutes() > 5 {
			idleWorkers = append(idleWorkers, inst)
		}
	}

	if len(idleWorkers) == 0 {
		return
	}

	surplus := float64(len(idleWorkers) - int(pool.MinIdleRunners))

	if surplus <= 0 {
		return
	}

	scaleDownFactor := 0.5 // could be configurable
	numScaleDown := int(math.Ceil(surplus * scaleDownFactor))

	if numScaleDown <= 0 || numScaleDown > len(idleWorkers) {
		log.Printf("invalid number of instances to scale down: %v, check your scaleDownFactor: %v\n", numScaleDown, scaleDownFactor)
		return
	}

	for _, instanceToDelete := range idleWorkers[:numScaleDown] {
		log.Printf("scaling down idle worker %s from pool %s\n", instanceToDelete.Name, pool.ID)
		if err := r.ForceDeleteRunner(instanceToDelete); err != nil {
			log.Printf("failed to delete instance %s: %s", instanceToDelete.ID, err)
		}
	}
}

func (r *basePoolManager) addRunnerToPool(pool params.Pool) error {
	r.runnerMux.Lock()
	defer r.runnerMux.Unlock()

	if !pool.Enabled {
		return nil
	}

	poolInstanceCount, err := r.store.PoolInstanceCount(r.ctx, pool.ID)
	if err != nil {
		return fmt.Errorf("failed to list pool instances: %w", err)
	}

	if poolInstanceCount >= int64(pool.MaxRunners) {
		return fmt.Errorf("max workers (%d) reached for pool %s", pool.MaxRunners, pool.ID)
	}

	if err := r.AddRunner(r.ctx, pool.ID); err != nil {
		return fmt.Errorf("failed to add new instance for pool %s: %s", pool.ID, err)
	}
	return nil
}

func (r *basePoolManager) ensureIdleRunnersForOnePool(pool params.Pool) error {
	r.runnerMux.Lock()
	defer r.runnerMux.Unlock()

	if !pool.Enabled {
		return nil
	}
	existingInstances, err := r.store.ListPoolInstances(r.ctx, pool.ID)
	if err != nil {
		return fmt.Errorf("failed to ensure minimum idle workers for pool %s: %s", pool.ID, err)

	}

	if uint(len(existingInstances)) >= pool.MaxRunners {
		return nil
	}

	idleOrPendingWorkers := []params.Instance{}
	for _, inst := range existingInstances {
		if providerCommon.RunnerStatus(inst.RunnerStatus) != providerCommon.RunnerActive {
			idleOrPendingWorkers = append(idleOrPendingWorkers, inst)
		}
	}

	var required int
	if len(idleOrPendingWorkers) < int(pool.MinIdleRunners) {
		// get the needed delta.
		required = int(pool.MinIdleRunners) - len(idleOrPendingWorkers)

		projectedInstanceCount := len(existingInstances) + required
		if uint(projectedInstanceCount) > pool.MaxRunners {
			// ensure we don't go above max workers
			delta := projectedInstanceCount - int(pool.MaxRunners)
			required = required - delta
		}
	}

	for i := 0; i < required; i++ {
		log.Printf("adding new idle worker to pool %s", pool.ID)
		if err := r.AddRunner(r.ctx, pool.ID); err != nil {
			return fmt.Errorf("failed to add new instance for pool %s: %s", pool.ID, err)
		}
	}
	return nil
}

func (r *basePoolManager) retryFailedInstancesForOnePool(pool params.Pool) {
	if !pool.Enabled {
		return
	}

	existingInstances, err := r.store.ListPoolInstances(r.ctx, pool.ID)
	if err != nil {
		log.Printf("retrying failed instances: failed to list instances for pool %s: %s", pool.ID, err)
		return
	}

	g, _ := errgroup.WithContext(r.ctx)
	for _, instance := range existingInstances {
		if instance.Status != providerCommon.InstanceError {
			continue
		}
		if instance.CreateAttempt >= maxCreateAttempts {
			continue
		}
		instance := instance
		g.Go(func() error {
			// NOTE(gabriel-samfira): this is done in parallel. If there are many failed instances
			// this has the potential to create many API requests to the target provider.
			// TODO(gabriel-samfira): implement request throttling.
			if err := r.deleteInstanceFromProvider(instance); err != nil {
				log.Printf("failed to delete instance %s from provider: %s", instance.Name, err)
				// Bail here, otherwise we risk creating multiple failing instances, and losing track
				// of them. If Create instance failed to return a proper provider ID, we rely on the
				// name to delete the instance. If we don't bail here, and end up with multiple
				// instances with the same name, using the name to clean up failed instances will fail
				// on any subsequent call, unless the external or native provider takes into account
				// non unique names and loops over all of them. Something which is extremely hacky and
				// which we would rather avoid.
				return err
			}

			// TODO(gabriel-samfira): Incrementing CreateAttempt should be done within a transaction.
			// It's fairly safe to do here (for now), as there should be no other code path that updates
			// an instance in this state.
			var tokenFetched bool = false
			updateParams := params.UpdateInstanceParams{
				CreateAttempt: instance.CreateAttempt + 1,
				TokenFetched:  &tokenFetched,
				Status:        providerCommon.InstancePendingCreate,
			}
			log.Printf("queueing previously failed instance %s for retry", instance.Name)
			// Set instance to pending create and wait for retry.
			if _, err := r.updateInstance(instance.Name, updateParams); err != nil {
				log.Printf("failed to update runner %s status", instance.Name)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		log.Printf("failed to retry failed instances for pool %s: %s", pool.ID, err)
	}
}

func (r *basePoolManager) retryFailedInstances() {
	r.runnerMux.Lock()
	defer r.runnerMux.Unlock()

	pools, err := r.helper.ListPools()
	if err != nil {
		log.Printf("error listing pools: %s", err)
		return
	}
	wg := sync.WaitGroup{}
	wg.Add(len(pools))
	for _, pool := range pools {
		go func(pool params.Pool) {
			defer wg.Done()
			r.retryFailedInstancesForOnePool(pool)
		}(pool)
	}
	wg.Wait()
}

func (r *basePoolManager) scaleDown() {
	r.runnerMux.Lock()
	defer r.runnerMux.Unlock()

	pools, err := r.helper.ListPools()
	if err != nil {
		log.Printf("error listing pools: %s", err)
		return
	}
	wg := sync.WaitGroup{}
	wg.Add(len(pools))
	for _, pool := range pools {
		go func(pool params.Pool) {
			defer wg.Done()
			r.scaleDownOnePool(pool)
		}(pool)
	}
	wg.Wait()
}

func (r *basePoolManager) ensureMinIdleRunners() {
	pools, err := r.helper.ListPools()
	if err != nil {
		log.Printf("error listing pools: %s", err)
		return
	}
	wg := sync.WaitGroup{}
	wg.Add(len(pools))
	for _, pool := range pools {
		go func(pool params.Pool) {
			defer wg.Done()
			if err := r.ensureIdleRunnersForOnePool(pool); err != nil {
				log.Printf("error ensuring idle runners for pool %s: %s", pool.ID, err)
			}
		}(pool)
	}
	wg.Wait()
}

func (r *basePoolManager) deleteInstanceFromProvider(instance params.Instance) error {
	pool, err := r.helper.GetPoolByID(instance.PoolID)
	if err != nil {
		return errors.Wrap(err, "fetching pool")
	}

	provider, ok := r.providers[pool.ProviderName]
	if !ok {
		return fmt.Errorf("unknown provider %s for pool %s", pool.ProviderName, pool.ID)
	}

	identifier := instance.ProviderID
	if identifier == "" {
		// provider did not return a provider ID?
		// try with name
		identifier = instance.Name
	}

	if err := provider.DeleteInstance(r.ctx, identifier); err != nil {
		return errors.Wrap(err, "removing instance")
	}

	return nil
}

func (r *basePoolManager) deletePendingInstances() {
	instances, err := r.helper.FetchDbInstances()
	if err != nil {
		log.Printf("failed to fetch instances from store: %s", err)
		return
	}
	g, ctx := errgroup.WithContext(r.ctx)
	for _, instance := range instances {
		if instance.Status != providerCommon.InstancePendingDelete {
			// not in pending_delete status. Skip.
			continue
		}

		// Set the status to deleting before launching the goroutine that removes
		// the runner from the provider (which can take a long time).
		if _, err := r.setInstanceStatus(instance.Name, providerCommon.InstanceDeleting, nil); err != nil {
			log.Printf("failed to update runner %s status", instance.Name)
		}
		instance := instance
		g.Go(func() (err error) {
			defer func(instance params.Instance) {
				if err != nil {
					// failed to remove from provider. Set the status back to pending_delete, which
					// will retry the operation.
					if _, err := r.setInstanceStatus(instance.Name, providerCommon.InstancePendingDelete, nil); err != nil {
						log.Printf("failed to update runner %s status", instance.Name)
					}
				}
			}(instance)

			err = r.deleteInstanceFromProvider(instance)
			if err != nil {
				return errors.Wrap(err, "removing instance from provider")
			}

			if deleteErr := r.store.DeleteInstance(ctx, instance.PoolID, instance.Name); deleteErr != nil {
				return errors.Wrap(deleteErr, "deleting instance from database")
			}
			return
		})
	}
	if err := g.Wait(); err != nil {
		log.Printf("failed to delete pending instances: %s", err)
	}
}

func (r *basePoolManager) addPendingInstances() {
	// TODO: filter instances by status.
	instances, err := r.helper.FetchDbInstances()
	if err != nil {
		log.Printf("failed to fetch instances from store: %s", err)
		return
	}
	g, _ := errgroup.WithContext(r.ctx)
	for _, instance := range instances {
		if instance.Status != providerCommon.InstancePendingCreate {
			// not in pending_create status. Skip.
			continue
		}
		// Set the instance to "creating" before launching the goroutine. This will ensure that addPendingInstances()
		// won't attempt to create the runner a second time.
		if _, err := r.setInstanceStatus(instance.Name, providerCommon.InstanceCreating, nil); err != nil {
			log.Printf("failed to update runner %s status: %s", instance.Name, err)
			// We failed to transition the instance to Creating. This means that garm will retry to create this instance
			// when the loop runs again and we end up with multiple instances.
			continue
		}
		instance := instance
		g.Go(func() error {
			log.Printf("creating instance %s in pool %s", instance.Name, instance.PoolID)
			if err := r.addInstanceToProvider(instance); err != nil {
				log.Printf("failed to add instance to provider: %s", err)
				errAsBytes := []byte(err.Error())
				if _, err := r.setInstanceStatus(instance.Name, providerCommon.InstanceError, errAsBytes); err != nil {
					log.Printf("failed to update runner %s status", instance.Name)
				}
				log.Printf("failed to create instance in provider: %s", err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		log.Printf("failed to add pending instances: %s", err)
	}
}

func (r *basePoolManager) consolidate() {
	// TODO(gabriel-samfira): replace this with something more efficient.
	r.mux.Lock()
	defer r.mux.Unlock()

	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.deletePendingInstances()
	}()
	go func() {
		defer wg.Done()
		r.addPendingInstances()
	}()
	wg.Wait()

	wg.Add(2)
	go func() {
		defer wg.Done()
		r.ensureMinIdleRunners()
	}()

	go func() {
		defer wg.Done()
		r.retryFailedInstances()
	}()
	wg.Wait()
}

func (r *basePoolManager) Wait() error {
	select {
	case <-r.done:
	case <-time.After(60 * time.Second):
		return errors.Wrap(runnerErrors.ErrTimeout, "waiting for pool to stop")
	}
	return nil
}

func (r *basePoolManager) runnerCleanup() error {
	runners, err := r.helper.GetGithubRunners()
	if err != nil {
		return errors.Wrap(err, "fetching github runners")
	}
	if err := r.cleanupOrphanedProviderRunners(runners); err != nil {
		return errors.Wrap(err, "cleaning orphaned instances")
	}

	if err := r.cleanupOrphanedGithubRunners(runners); err != nil {
		return errors.Wrap(err, "cleaning orphaned github runners")
	}
	return nil
}

func (r *basePoolManager) Start() error {
	go r.loop()
	return nil
}

func (r *basePoolManager) Stop() error {
	close(r.quit)
	return nil
}

func (r *basePoolManager) RefreshState(param params.UpdatePoolStateParams) error {
	return r.helper.UpdateState(param)
}

func (r *basePoolManager) WebhookSecret() string {
	return r.helper.WebhookSecret()
}

func (r *basePoolManager) GithubRunnerRegistrationToken() (string, error) {
	return r.helper.GetGithubRegistrationToken()
}

func (r *basePoolManager) ID() string {
	return r.helper.ID()
}

func (r *basePoolManager) ForceDeleteRunner(runner params.Instance) error {
	if !r.managerIsRunning {
		return runnerErrors.NewConflictError("pool manager is not running for %s", r.helper.String())
	}
	if runner.AgentID != 0 {
		resp, err := r.helper.RemoveGithubRunner(runner.AgentID)
		if err != nil {
			if resp != nil {
				switch resp.StatusCode {
				case http.StatusUnprocessableEntity:
					return errors.Wrapf(runnerErrors.ErrBadRequest, "removing runner: %q", err)
				case http.StatusNotFound:
					// Runner may have been deleted by a finished job, or manually by the user.
					log.Printf("runner with agent id %d was not found in github", runner.AgentID)
				case http.StatusUnauthorized:
					// Mark the pool as offline from this point forward
					failureReason := fmt.Sprintf("failed to remove runner: %q", err)
					r.setPoolRunningState(false, failureReason)
					log.Print(failureReason)
					// evaluate the next switch case.
					fallthrough
				default:
					return errors.Wrap(err, "removing runner")
				}
			} else {
				// We got a nil response. Assume we are in error.
				return errors.Wrap(err, "removing runner")
			}
		}
	}
	log.Printf("setting instance status for: %v", runner.Name)

	if _, err := r.setInstanceStatus(runner.Name, providerCommon.InstancePendingDelete, nil); err != nil {
		log.Printf("failed to update runner %s status", runner.Name)
		return errors.Wrap(err, "updating runner")
	}
	return nil
}

// consumeQueuedJobs qull pull all the known jobs from the database and attempt to create a new
// runner in one of the pools it manages if it matches the requested labels.
// This is a best effort attempt to consume queued jobs. We do not have any real way to know which
// runner from which pool will pick up a job we react to here. For example, the same job may be received
// by an enterprise manager, an org manager AND a repo manager. If an idle runner from another pool
// picks up the job after we created a runner in this pool, we will have an extra runner that may or may not
// have a job waiting for it.
// This is not a huge problem, as we have scale down logic which should remove any idle runners that have not
// picked up a job within a certain time frame. Also, the logic here should ensure that eventually, all known
// queued jobs will be consumed sooner or later.
//
// NOTE: jobs that were created while the garm instance was down, will be unknown to garm itself and will linger
// in queued state if the pools defined in garm have a minimum idle runner value set to 0. Simply put, garm won't
// know about the queued jobs that we didn't get a webhook for. Listing all jobs on startup is not feasible, as
// an enterprise may have thousands of repos and thousands of jobs in queued state. To fetch all jobs for an
// enterprise, we'd have to list all repos, and for each repo list all jobs currently in queued state. This is
// not desirable by any measure.
func (r *basePoolManager) consumeQueuedJobs(ctx context.Context) error {
	queued, err := r.store.ListEntityJobsByStatus(ctx, r.helper.PoolType(), r.helper.ID(), params.JobStatusQueued)
	if err != nil {
		return errors.Wrap(err, "listing queued jobs")
	}

	log.Printf("found %d queued jobs", len(queued))
	for _, job := range queued {
		if job.LockedBy != uuid.Nil && job.LockedBy.String() != r.ID() {
			// Job was handled by us or another entity.
			log.Printf("[Pool mgr ID %s] job %d is locked by %s", r.ID(), job.ID, job.LockedBy.String())
			continue
		}

		if time.Since(job.CreatedAt) < time.Second*15 {
			// give the idle runners a chance to pick up the job.
			log.Printf("job %d was created less than 15 seconds ago. Skipping", job.ID)
			continue
		}

		if time.Since(job.CreatedAt) >= time.Minute*5 {
			// Job has been in queued state for 30 minutes or more. Check if it was consumed by another runner.
			workflow, ghResp, err := r.helper.GithubCLI().GetWorkflowJobByID(r.ctx, job.RepositoryOwner, job.RepositoryName, job.ID)
			if err != nil {
				if ghResp != nil {
					switch ghResp.StatusCode {
					case http.StatusNotFound:
						// Job does not exist in github. Remove it from the database.
						if err := r.store.DeleteJob(ctx, job.ID); err != nil {
							return errors.Wrap(err, "deleting job")
						}
					default:
						log.Printf("failed to fetch job information from github: %q (status code: %d)", err, ghResp.StatusCode)
					}
				}
				log.Printf("error fetching workflow info: %q", err)
				continue
			}

			if workflow.GetStatus() != "queued" {
				log.Printf("job is no longer in queued state on github. New status is: %s", workflow.GetStatus())
				job.Action = workflow.GetStatus()
				job.Status = workflow.GetStatus()
				job.Conclusion = workflow.GetConclusion()
				if workflow.RunnerName != nil {
					job.RunnerName = *workflow.RunnerName
				}
				if workflow.RunnerID != nil {
					job.GithubRunnerID = *workflow.RunnerID
				}
				if workflow.RunnerGroupName != nil {
					job.RunnerGroupName = *workflow.RunnerGroupName
				}
				if workflow.RunnerGroupID != nil {
					job.RunnerGroupID = *workflow.RunnerGroupID
				}
				if _, err := r.store.CreateOrUpdateJob(ctx, job); err != nil {
					log.Printf("failed to update job status: %q", err)
				}
				continue
			}

			// Job is still queued in our db and in github. Unlock it and try again.
			if err := r.store.UnlockJob(ctx, job.ID, r.ID()); err != nil {
				// TODO: Implament a cache? Should we return here?
				log.Printf("failed to unlock job: %q", err)
				continue
			}
		}

		potentialPools, err := r.store.FindPoolsMatchingAllTags(ctx, r.helper.PoolType(), r.helper.ID(), job.Labels)
		if err != nil {
			log.Printf("[Pool mgr ID %s] error finding pools matching labels: %s", r.ID(), err)
			continue
		}

		if len(potentialPools) == 0 {
			log.Printf("[Pool mgr ID %s] could not find pool with labels %s", r.ID(), strings.Join(job.Labels, ","))
			continue
		}

		runnerCreated := false
		if err := r.store.LockJob(ctx, job.ID, r.ID()); err != nil {
			log.Printf("[Pool mgr ID %s] could not lock job %d: %s", r.ID(), job.ID, err)
			continue
		}
		for _, pool := range potentialPools {
			log.Printf("attempting to create a runner in pool %s for job %d", pool.ID, job.ID)
			if err := r.addRunnerToPool(pool); err != nil {
				log.Printf("could not add runner to pool %s: %s", pool.ID, err)
				continue
			}
			log.Printf("a new runner was added to pool %s as a response to queued job %d", pool.ID, job.ID)
			runnerCreated = true
			break
		}
		if !runnerCreated {
			log.Printf("could not create a runner for job %d; unlocking", job.ID)
			if err := r.store.UnlockJob(ctx, job.ID, r.ID()); err != nil {
				return errors.Wrap(err, "unlocking job")
			}
		}
	}
	return nil
}
