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

	"github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/util"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func (s *sqlDatabase) sqlToParamsInstance(instance Instance) params.Instance {
	var id string
	if instance.ProviderID != nil {
		id = *instance.ProviderID
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
		GitHubRunnerGroup: instance.GitHubRunnerGroup,
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
	return ret
}

func (s *sqlDatabase) sqlAddressToParamsAddress(addr Address) params.Address {
	return params.Address{
		Address: addr.Address,
		Type:    params.AddressType(addr.Type),
	}
}

func (s *sqlDatabase) sqlToCommonOrganization(org Organization) (params.Organization, error) {
	if len(org.WebhookSecret) == 0 {
		return params.Organization{}, errors.New("missing secret")
	}
	secret, err := util.Aes256DecodeString(org.WebhookSecret, s.cfg.Passphrase)
	if err != nil {
		return params.Organization{}, errors.Wrap(err, "decrypting secret")
	}

	ret := params.Organization{
		ID:              org.ID.String(),
		Name:            org.Name,
		CredentialsName: org.CredentialsName,
		Pools:           make([]params.Pool, len(org.Pools)),
		WebhookSecret:   secret,
	}

	for idx, pool := range org.Pools {
		ret.Pools[idx] = s.sqlToCommonPool(pool)
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToCommonEnterprise(enterprise Enterprise) (params.Enterprise, error) {
	if len(enterprise.WebhookSecret) == 0 {
		return params.Enterprise{}, errors.New("missing secret")
	}
	secret, err := util.Aes256DecodeString(enterprise.WebhookSecret, s.cfg.Passphrase)
	if err != nil {
		return params.Enterprise{}, errors.Wrap(err, "decrypting secret")
	}

	ret := params.Enterprise{
		ID:              enterprise.ID.String(),
		Name:            enterprise.Name,
		CredentialsName: enterprise.CredentialsName,
		Pools:           make([]params.Pool, len(enterprise.Pools)),
		WebhookSecret:   secret,
	}

	for idx, pool := range enterprise.Pools {
		ret.Pools[idx] = s.sqlToCommonPool(pool)
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToCommonPool(pool Pool) params.Pool {
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
	}

	if pool.RepoID != uuid.Nil {
		ret.RepoID = pool.RepoID.String()
		if pool.Repository.Owner != "" && pool.Repository.Name != "" {
			ret.RepoName = fmt.Sprintf("%s/%s", pool.Repository.Owner, pool.Repository.Name)
		}
	}

	if pool.OrgID != uuid.Nil && pool.Organization.Name != "" {
		ret.OrgID = pool.OrgID.String()
		ret.OrgName = pool.Organization.Name
	}

	if pool.EnterpriseID != uuid.Nil && pool.Enterprise.Name != "" {
		ret.EnterpriseID = pool.EnterpriseID.String()
		ret.EnterpriseName = pool.Enterprise.Name
	}

	for idx, val := range pool.Tags {
		ret.Tags[idx] = s.sqlToCommonTags(*val)
	}

	for idx, inst := range pool.Instances {
		ret.Instances[idx] = s.sqlToParamsInstance(inst)
	}

	return ret
}

func (s *sqlDatabase) sqlToCommonTags(tag Tag) params.Tag {
	return params.Tag{
		ID:   tag.ID.String(),
		Name: tag.Name,
	}
}

func (s *sqlDatabase) sqlToCommonRepository(repo Repository) (params.Repository, error) {
	if len(repo.WebhookSecret) == 0 {
		return params.Repository{}, errors.New("missing secret")
	}
	secret, err := util.Aes256DecodeString(repo.WebhookSecret, s.cfg.Passphrase)
	if err != nil {
		return params.Repository{}, errors.Wrap(err, "decrypting secret")
	}

	ret := params.Repository{
		ID:              repo.ID.String(),
		Name:            repo.Name,
		Owner:           repo.Owner,
		CredentialsName: repo.CredentialsName,
		Pools:           make([]params.Pool, len(repo.Pools)),
		WebhookSecret:   secret,
	}

	for idx, pool := range repo.Pools {
		ret.Pools[idx] = s.sqlToCommonPool(pool)
	}

	return ret, nil
}

func (s *sqlDatabase) sqlToParamsUser(user User) params.User {
	return params.User{
		ID:        user.ID.String(),
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		Email:     user.Email,
		Username:  user.Username,
		FullName:  user.FullName,
		Password:  user.Password,
		Enabled:   user.Enabled,
		IsAdmin:   user.IsAdmin,
	}
}

func (s *sqlDatabase) getOrCreateTag(tagName string) (Tag, error) {
	var tag Tag
	q := s.conn.Where("name = ?", tagName).First(&tag)
	if q.Error == nil {
		return tag, nil
	}
	if !errors.Is(q.Error, gorm.ErrRecordNotFound) {
		return Tag{}, errors.Wrap(q.Error, "fetching tag from database")
	}
	newTag := Tag{
		Name: tagName,
	}

	q = s.conn.Create(&newTag)
	if q.Error != nil {
		return Tag{}, errors.Wrap(q.Error, "creating tag")
	}
	return newTag, nil
}

func (s *sqlDatabase) updatePool(pool Pool, param params.UpdatePoolParams) (params.Pool, error) {
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

	if q := s.conn.Save(&pool); q.Error != nil {
		return params.Pool{}, errors.Wrap(q.Error, "saving database entry")
	}

	tags := []Tag{}
	if param.Tags != nil && len(param.Tags) > 0 {
		for _, val := range param.Tags {
			t, err := s.getOrCreateTag(val)
			if err != nil {
				return params.Pool{}, errors.Wrap(err, "fetching tag")
			}
			tags = append(tags, t)
		}

		if err := s.conn.Model(&pool).Association("Tags").Replace(&tags); err != nil {
			return params.Pool{}, errors.Wrap(err, "replacing tags")
		}
	}

	return s.sqlToCommonPool(pool), nil
}
