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

package sql

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	runnerErrors "github.com/cloudbase/garm-provider-common/errors"
	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm-provider-common/util"
	dbCommon "github.com/cloudbase/garm/database/common"
	"github.com/cloudbase/garm/params"
)

func (s *sqlDatabase) sqlToParamsInstance(instance Instance) (params.Instance, error) {
	var id string
	if instance.ProviderID != nil {
		id = *instance.ProviderID
	}

	var labels []string
	if len(instance.AditionalLabels) > 0 {
		if err := json.Unmarshal(instance.AditionalLabels, &labels); err != nil {
			return params.Instance{}, errors.Wrap(err, "unmarshalling labels")
		}
	}

	var jitConfig map[string]string
	if len(instance.JitConfiguration) > 0 {
		if err := s.unsealAndUnmarshal(instance.JitConfiguration, &jitConfig); err != nil {
			return params.Instance{}, errors.Wrap(err, "unmarshalling jit configuration")
		}
	}
	ret := params.Instance{
		ID:                instance.ID.String(),
		ProviderID:        id,
		AgentID:           instance.AgentID,
		Name:              instance.Name,
		OSType:            instance.OSType,
		OSName:            instance.OSName,
		OSVersion:         instance.OSVersion,
		OSArch:            instance.OSArch,
		Status:            instance.Status,
		RunnerStatus:      instance.RunnerStatus,
		PoolID:            instance.PoolID.String(),
		CallbackURL:       instance.CallbackURL,
		MetadataURL:       instance.MetadataURL,
		StatusMessages:    []params.StatusMessage{},
		CreateAttempt:     instance.CreateAttempt,
		UpdatedAt:         instance.UpdatedAt,
		TokenFetched:      instance.TokenFetched,
		JitConfiguration:  jitConfig,
		GitHubRunnerGroup: instance.GitHubRunnerGroup,
		AditionalLabels:   labels,
	}

	if instance.Job != nil {
		paramJob, err := sqlWorkflowJobToParamsJob(*instance.Job)
		if err != nil {
			return params.Instance{}, errors.Wrap(err, "converting job")
		}
		ret.Job = &paramJob
	}

	if len(instance.ProviderFault) > 0 {
		ret.ProviderFault = instance.ProviderFault
	}

	for _, addr := range instance.Addresses {
		ret.Addresses = append(ret.Addresses, s.sqlAddressToParamsAddress(addr))
	}

	for _, msg := range instance.StatusMessages {
		ret.StatusMessages = append(ret.StatusMessages, params.StatusMessage{
			CreatedAt:  msg.CreatedAt,
			Message:    msg.Message,
			EventType:  msg.EventType,
			EventLevel: msg.EventLevel,
		})
	}
	return ret, nil
}

func (s *sqlDatabase) sqlAddressToParamsAddress(addr Address) commonParams.Address {
	return commonParams.Address{
		Address: addr.Address,
		Type:    commonParams.AddressType(addr.Type),
	}
}

func (s *sqlDatabase) sqlToCommonOrganization(org Organization, detailed bool) (params.Organization, error) {
	if len(org.WebhookSecret) == 0 {
		return params.Organization{}, errors.New("missing secret")
	}
	secret, err := util.Unseal(org.WebhookSecret, []byte(s.cfg.Passphrase))
	if err != nil {
		return params.Organization{}, errors.Wrap(err, "decrypting secret")
	}

	endpoint, err := s.sqlToCommonGithubEndpoint(org.Endpoint)
	if err != nil {
		return params.Organization{}, errors.Wrap(err, "converting endpoint")
	}
	ret := params.Organization{
		ID:               org.ID.String(),
		Name:             org.Name,
		CredentialsName:  org.Credentials.Name,
		Pools:            make([]params.Pool, len(org.Pools)),
		WebhookSecret:    string(secret),
		PoolBalancerType: org.PoolBalancerType,
		Endpoint:         endpoint,
	}

	if org.CredentialsID != nil {
		ret.CredentialsID = *org.CredentialsID
	}

	if detailed {
		creds, err := s.sqlToCommonGithubCredentials(org.Credentials)
		if err != nil {
			return params.Organization{}, errors.Wrap(err, "converting credentials")
		}
		ret.Credentials = creds
	}

	if ret.PoolBalancerType == "" {
		ret.PoolBalancerType = params.PoolBalancerTypeRoundRobin
	}

	for idx, pool := range org.Pools {
		ret.Pools[idx], err = s.sqlToCommonPool(pool)
		if err != nil {
			return params.Organization{}, errors.Wrap(err, "converting pool")
		}
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToCommonEnterprise(enterprise Enterprise, detailed bool) (params.Enterprise, error) {
	if len(enterprise.WebhookSecret) == 0 {
		return params.Enterprise{}, errors.New("missing secret")
	}
	secret, err := util.Unseal(enterprise.WebhookSecret, []byte(s.cfg.Passphrase))
	if err != nil {
		return params.Enterprise{}, errors.Wrap(err, "decrypting secret")
	}

	endpoint, err := s.sqlToCommonGithubEndpoint(enterprise.Endpoint)
	if err != nil {
		return params.Enterprise{}, errors.Wrap(err, "converting endpoint")
	}
	ret := params.Enterprise{
		ID:               enterprise.ID.String(),
		Name:             enterprise.Name,
		CredentialsName:  enterprise.Credentials.Name,
		Pools:            make([]params.Pool, len(enterprise.Pools)),
		WebhookSecret:    string(secret),
		PoolBalancerType: enterprise.PoolBalancerType,
		Endpoint:         endpoint,
	}

	if enterprise.CredentialsID != nil {
		ret.CredentialsID = *enterprise.CredentialsID
	}

	if detailed {
		creds, err := s.sqlToCommonGithubCredentials(enterprise.Credentials)
		if err != nil {
			return params.Enterprise{}, errors.Wrap(err, "converting credentials")
		}
		ret.Credentials = creds
	}

	if ret.PoolBalancerType == "" {
		ret.PoolBalancerType = params.PoolBalancerTypeRoundRobin
	}

	for idx, pool := range enterprise.Pools {
		ret.Pools[idx], err = s.sqlToCommonPool(pool)
		if err != nil {
			return params.Enterprise{}, errors.Wrap(err, "converting pool")
		}
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToCommonPool(pool Pool) (params.Pool, error) {
	ret := params.Pool{
		ID:             pool.ID.String(),
		ProviderName:   pool.ProviderName,
		MaxRunners:     pool.MaxRunners,
		MinIdleRunners: pool.MinIdleRunners,
		RunnerPrefix: params.RunnerPrefix{
			Prefix: pool.RunnerPrefix,
		},
		Image:                  pool.Image,
		Flavor:                 pool.Flavor,
		OSArch:                 pool.OSArch,
		OSType:                 pool.OSType,
		Enabled:                pool.Enabled,
		Tags:                   make([]params.Tag, len(pool.Tags)),
		Instances:              make([]params.Instance, len(pool.Instances)),
		RunnerBootstrapTimeout: pool.RunnerBootstrapTimeout,
		ExtraSpecs:             json.RawMessage(pool.ExtraSpecs),
		GitHubRunnerGroup:      pool.GitHubRunnerGroup,
		Priority:               pool.Priority,
	}

	if pool.RepoID != nil {
		ret.RepoID = pool.RepoID.String()
		if pool.Repository.Owner != "" && pool.Repository.Name != "" {
			ret.RepoName = fmt.Sprintf("%s/%s", pool.Repository.Owner, pool.Repository.Name)
		}
	}

	if pool.OrgID != nil && pool.Organization.Name != "" {
		ret.OrgID = pool.OrgID.String()
		ret.OrgName = pool.Organization.Name
	}

	if pool.EnterpriseID != nil && pool.Enterprise.Name != "" {
		ret.EnterpriseID = pool.EnterpriseID.String()
		ret.EnterpriseName = pool.Enterprise.Name
	}

	for idx, val := range pool.Tags {
		ret.Tags[idx] = s.sqlToCommonTags(*val)
	}

	var err error
	for idx, inst := range pool.Instances {
		ret.Instances[idx], err = s.sqlToParamsInstance(inst)
		if err != nil {
			return params.Pool{}, errors.Wrap(err, "converting instance")
		}
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToCommonTags(tag Tag) params.Tag {
	return params.Tag{
		ID:   tag.ID.String(),
		Name: tag.Name,
	}
}

func (s *sqlDatabase) sqlToCommonRepository(repo Repository, detailed bool) (params.Repository, error) {
	if len(repo.WebhookSecret) == 0 {
		return params.Repository{}, errors.New("missing secret")
	}
	secret, err := util.Unseal(repo.WebhookSecret, []byte(s.cfg.Passphrase))
	if err != nil {
		return params.Repository{}, errors.Wrap(err, "decrypting secret")
	}
	endpoint, err := s.sqlToCommonGithubEndpoint(repo.Endpoint)
	if err != nil {
		return params.Repository{}, errors.Wrap(err, "converting endpoint")
	}
	ret := params.Repository{
		ID:               repo.ID.String(),
		Name:             repo.Name,
		Owner:            repo.Owner,
		CredentialsName:  repo.Credentials.Name,
		Pools:            make([]params.Pool, len(repo.Pools)),
		WebhookSecret:    string(secret),
		PoolBalancerType: repo.PoolBalancerType,
		Endpoint:         endpoint,
	}

	if repo.CredentialsID != nil {
		ret.CredentialsID = *repo.CredentialsID
	}

	if detailed {
		creds, err := s.sqlToCommonGithubCredentials(repo.Credentials)
		if err != nil {
			return params.Repository{}, errors.Wrap(err, "converting credentials")
		}
		ret.Credentials = creds
	}

	if ret.PoolBalancerType == "" {
		ret.PoolBalancerType = params.PoolBalancerTypeRoundRobin
	}

	for idx, pool := range repo.Pools {
		ret.Pools[idx], err = s.sqlToCommonPool(pool)
		if err != nil {
			return params.Repository{}, errors.Wrap(err, "converting pool")
		}
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToParamsUser(user User) params.User {
	return params.User{
		ID:         user.ID.String(),
		CreatedAt:  user.CreatedAt,
		UpdatedAt:  user.UpdatedAt,
		Email:      user.Email,
		Username:   user.Username,
		FullName:   user.FullName,
		Password:   user.Password,
		Enabled:    user.Enabled,
		IsAdmin:    user.IsAdmin,
		Generation: user.Generation,
	}
}

func (s *sqlDatabase) getOrCreateTag(tx *gorm.DB, tagName string) (Tag, error) {
	var tag Tag
	q := tx.Where("name = ?", tagName).First(&tag)
	if q.Error == nil {
		return tag, nil
	}
	if !errors.Is(q.Error, gorm.ErrRecordNotFound) {
		return Tag{}, errors.Wrap(q.Error, "fetching tag from database")
	}
	newTag := Tag{
		Name: tagName,
	}

	if err := tx.Create(&newTag).Error; err != nil {
		return Tag{}, errors.Wrap(err, "creating tag")
	}
	return newTag, nil
}

var dbMutex sync.Mutex

func (s *sqlDatabase) updatePool(tx *gorm.DB, pool Pool, param params.UpdatePoolParams) (params.Pool, error) {
	if param.Enabled != nil && pool.Enabled != *param.Enabled {
		pool.Enabled = *param.Enabled
	}

	if param.Flavor != "" {
		pool.Flavor = param.Flavor
	}

	if param.Image != "" {
		pool.Image = param.Image
	}

	if param.Prefix != "" {
		pool.RunnerPrefix = param.Prefix
	}

	if param.MaxRunners != nil {
		pool.MaxRunners = *param.MaxRunners
	}

	if param.MinIdleRunners != nil {
		pool.MinIdleRunners = *param.MinIdleRunners
	}

	if param.OSArch != "" {
		pool.OSArch = param.OSArch
	}

	if param.OSType != "" {
		pool.OSType = param.OSType
	}

	if param.ExtraSpecs != nil {
		pool.ExtraSpecs = datatypes.JSON(param.ExtraSpecs)
	}

	if param.RunnerBootstrapTimeout != nil && *param.RunnerBootstrapTimeout > 0 {
		pool.RunnerBootstrapTimeout = *param.RunnerBootstrapTimeout
	}

	if param.GitHubRunnerGroup != nil {
		pool.GitHubRunnerGroup = *param.GitHubRunnerGroup
	}

	if param.Priority != nil {
		pool.Priority = *param.Priority
	}


	dbMutex.Lock()
	if q := tx.Save(&pool); q.Error != nil {
		return params.Pool{}, errors.Wrap(q.Error, "saving database entry")
	}
	dbMutex.Unlock()

	tags := []Tag{}
	if param.Tags != nil && len(param.Tags) > 0 {
		for _, val := range param.Tags {
			t, err := s.getOrCreateTag(tx, val)
			if err != nil {
				return params.Pool{}, errors.Wrap(err, "fetching tag")
			}
			tags = append(tags, t)
		}

		if err := tx.Model(&pool).Association("Tags").Replace(&tags); err != nil {
			return params.Pool{}, errors.Wrap(err, "replacing tags")
		}
	}

	return s.sqlToCommonPool(pool)
}

func (s *sqlDatabase) getPoolByID(tx *gorm.DB, poolID string, preload ...string) (Pool, error) {
	u, err := uuid.Parse(poolID)
	if err != nil {
		return Pool{}, errors.Wrap(runnerErrors.ErrBadRequest, "parsing id")
	}
	var pool Pool
	q := tx.Model(&Pool{})
	if len(preload) > 0 {
		for _, item := range preload {
			q = q.Preload(item)
		}
	}

	q = q.Where("id = ?", u).First(&pool)

	if q.Error != nil {
		if errors.Is(q.Error, gorm.ErrRecordNotFound) {
			return Pool{}, runnerErrors.ErrNotFound
		}
		return Pool{}, errors.Wrap(q.Error, "fetching org from database")
	}
	return pool, nil
}

func (s *sqlDatabase) hasGithubEntity(tx *gorm.DB, entityType params.GithubEntityType, entityID string) error {
	u, err := uuid.Parse(entityID)
	if err != nil {
		return errors.Wrap(runnerErrors.ErrBadRequest, "parsing id")
	}
	var q *gorm.DB
	switch entityType {
	case params.GithubEntityTypeRepository:
		q = tx.Model(&Repository{}).Where("id = ?", u)
	case params.GithubEntityTypeOrganization:
		q = tx.Model(&Organization{}).Where("id = ?", u)
	case params.GithubEntityTypeEnterprise:
		q = tx.Model(&Enterprise{}).Where("id = ?", u)
	default:
		return errors.Wrap(runnerErrors.ErrBadRequest, "invalid entity type")
	}

	var entity interface{}
	if err := q.First(entity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.Wrap(runnerErrors.ErrNotFound, "entity not found")
		}
		return errors.Wrap(err, "fetching entity from database")
	}
	return nil
}

func (s *sqlDatabase) marshalAndSeal(data interface{}) ([]byte, error) {
	enc, err := json.Marshal(data)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling data")
	}
	return util.Seal(enc, []byte(s.cfg.Passphrase))
}

func (s *sqlDatabase) unsealAndUnmarshal(data []byte, target interface{}) error {
	decrypted, err := util.Unseal(data, []byte(s.cfg.Passphrase))
	if err != nil {
		return errors.Wrap(err, "decrypting data")
	}
	if err := json.Unmarshal(decrypted, target); err != nil {
		return errors.Wrap(err, "unmarshalling data")
	}
	return nil
}

func (s *sqlDatabase) sendNotify(entityType dbCommon.DatabaseEntityType, op dbCommon.OperationType, payload interface{}) error {
	if s.producer == nil {
		// no producer was registered. Not sending notifications.
		return nil
	}
	if payload == nil {
		return errors.New("missing payload")
	}
	message := dbCommon.ChangePayload{
		Operation:  op,
		Payload:    payload,
		EntityType: entityType,
	}
	return s.producer.Notify(message)
}
