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
	"context"
	"fmt"

	runnerErrors "github.com/cloudbase/garm/errors"
	"github.com/cloudbase/garm/params"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func (s *sqlDatabase) ListAllPools(ctx context.Context) ([]params.Pool, error) {
	var pools []Pool

	q := s.conn.Model(&Pool{}).
		Preload("Tags").
		Preload("Organization").
		Preload("Repository").
		Preload("Enterprise").
		Omit("extra_specs").
		Find(&pools)
	if q.Error != nil {
		return nil, errors.Wrap(q.Error, "fetching all pools")
	}

	ret := make([]params.Pool, len(pools))
	for idx, val := range pools {
		ret[idx] = s.sqlToCommonPool(val)
	}
	return ret, nil
}

func (s *sqlDatabase) GetPoolByID(ctx context.Context, poolID string) (params.Pool, error) {
	pool, err := s.getPoolByID(ctx, poolID, "Tags", "Instances", "Enterprise", "Organization", "Repository")
	if err != nil {
		return params.Pool{}, errors.Wrap(err, "fetching pool by ID")
	}
	return s.sqlToCommonPool(pool), nil
}

func (s *sqlDatabase) DeletePoolByID(ctx context.Context, poolID string) error {
	pool, err := s.getPoolByID(ctx, poolID)
	if err != nil {
		return errors.Wrap(err, "fetching pool by ID")
	}

	if q := s.conn.Unscoped().Delete(&pool); q.Error != nil {
		return errors.Wrap(q.Error, "removing pool")
	}

	return nil
}

func (s *sqlDatabase) getEntityPool(ctx context.Context, entityType params.PoolType, entityID, poolID string, preload ...string) (Pool, error) {
	if entityID == "" {
		return Pool{}, errors.Wrap(runnerErrors.ErrBadRequest, "missing entity id")
	}

	u, err := uuid.Parse(poolID)
	if err != nil {
		return Pool{}, errors.Wrap(runnerErrors.ErrBadRequest, "parsing id")
	}

	q := s.conn
	if len(preload) > 0 {
		for _, item := range preload {
			q = q.Preload(item)
		}
	}

	var fieldName string
	switch entityType {
	case params.RepositoryPool:
		fieldName = "repo_id"
	case params.OrganizationPool:
		fieldName = "org_id"
	case params.EnterprisePool:
		fieldName = "enterprise_id"
	default:
		return Pool{}, fmt.Errorf("invalid entityType: %v", entityType)
	}

	var pool Pool
	condition := fmt.Sprintf("id = ? and %s = ?", fieldName)
	err = q.Model(&Pool{}).
		Where(condition, u, entityID).
		First(&pool).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Pool{}, errors.Wrap(runnerErrors.ErrNotFound, "finding pool")
		}
		return Pool{}, errors.Wrap(err, "fetching pool")
	}

	return pool, nil
}

func (s *sqlDatabase) listEntityPools(ctx context.Context, entityType params.PoolType, entityID string, preload ...string) ([]Pool, error) {
	if _, err := uuid.Parse(entityID); err != nil {
		return nil, errors.Wrap(runnerErrors.ErrBadRequest, "parsing id")
	}

	q := s.conn
	if len(preload) > 0 {
		for _, item := range preload {
			q = q.Preload(item)
		}
	}

	var fieldName string
	switch entityType {
	case params.RepositoryPool:
		fieldName = "repo_id"
	case params.OrganizationPool:
		fieldName = "org_id"
	case params.EnterprisePool:
		fieldName = "enterprise_id"
	default:
		return nil, fmt.Errorf("invalid entityType: %v", entityType)
	}

	var pools []Pool
	condition := fmt.Sprintf("%s = ?", fieldName)
	err := q.Model(&Pool{}).
		Where(condition, entityID).
		Omit("extra_specs").
		Find(&pools).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []Pool{}, nil
		}
		return nil, errors.Wrap(err, "fetching pool")
	}

	return pools, nil
}

func (s *sqlDatabase) findPoolByTags(id string, poolType params.PoolType, tags []string) ([]params.Pool, error) {
	if len(tags) == 0 {
		return nil, runnerErrors.NewBadRequestError("missing tags")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, errors.Wrap(runnerErrors.ErrBadRequest, "parsing id")
	}

	var fieldName string
	switch poolType {
	case params.RepositoryPool:
		fieldName = "repo_id"
	case params.OrganizationPool:
		fieldName = "org_id"
	case params.EnterprisePool:
		fieldName = "enterprise_id"
	default:
		return nil, fmt.Errorf("invalid poolType: %v", poolType)
	}

	var pools []Pool
	where := fmt.Sprintf("tags.name in ? and %s = ? and enabled = true", fieldName)
	q := s.conn.Joins("JOIN pool_tags on pool_tags.pool_id=pools.id").
		Joins("JOIN tags on tags.id=pool_tags.tag_id").
		Group("pools.id").
		Preload("Tags").
		Having("count(1) = ?", len(tags)).
		Where(where, tags, u).Find(&pools)

	if q.Error != nil {
		if errors.Is(q.Error, gorm.ErrRecordNotFound) {
			return nil, runnerErrors.ErrNotFound
		}
		return nil, errors.Wrap(q.Error, "fetching pool")
	}

	if len(pools) == 0 {
		return nil, runnerErrors.ErrNotFound
	}

	ret := make([]params.Pool, len(pools))
	for idx, val := range pools {
		ret[idx] = s.sqlToCommonPool(val)
	}

	return ret, nil
}

func (s *sqlDatabase) FindPoolsMatchingAllTags(ctx context.Context, entityType params.PoolType, entityID string, tags []string) ([]params.Pool, error) {
	if len(tags) == 0 {
		return nil, runnerErrors.NewBadRequestError("missing tags")
	}

	pools, err := s.findPoolByTags(entityID, entityType, tags)
	if err != nil {
		return nil, errors.Wrap(err, "fetching pools")
	}

	return pools, nil
}
