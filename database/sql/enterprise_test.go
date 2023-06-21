// Copyright 2022 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.enterprise/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package sql

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"testing"

	"github.com/cloudbase/garm/params"

	dbCommon "github.com/cloudbase/garm/database/common"
	runnerErrors "github.com/cloudbase/garm/errors"
	garmTesting "github.com/cloudbase/garm/internal/testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/DATA-DOG/go-sqlmock.v1"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type EnterpriseTestFixtures struct {
	Enterprises            []params.Enterprise
	CreateEnterpriseParams params.CreateEnterpriseParams
	CreatePoolParams       params.CreatePoolParams
	CreateInstanceParams   params.CreateInstanceParams
	UpdateRepoParams       params.UpdateRepositoryParams
	UpdatePoolParams       params.UpdatePoolParams
	SQLMock                sqlmock.Sqlmock
}

type EnterpriseTestSuite struct {
	suite.Suite
	Store          dbCommon.Store
	StoreSQLMocked *sqlDatabase
	Fixtures       *EnterpriseTestFixtures
}

func (s *EnterpriseTestSuite) equalInstancesByName(expected, actual []params.Instance) {
	s.Require().Equal(len(expected), len(actual))

	sort.Slice(expected, func(i, j int) bool { return expected[i].Name > expected[j].Name })
	sort.Slice(actual, func(i, j int) bool { return actual[i].Name > actual[j].Name })

	for i := 0; i < len(expected); i++ {
		s.Require().Equal(expected[i].Name, actual[i].Name)
	}
}

func (s *EnterpriseTestSuite) assertSQLMockExpectations() {
	err := s.Fixtures.SQLMock.ExpectationsWereMet()
	if err != nil {
		s.FailNow(fmt.Sprintf("failed to meet sqlmock expectations, got error: %v", err))
	}
}

func (s *EnterpriseTestSuite) SetupTest() {
	// create testing sqlite database
	db, err := NewSQLDatabase(context.Background(), garmTesting.GetTestSqliteDBConfig(s.T()))
	if err != nil {
		s.FailNow(fmt.Sprintf("failed to create db connection: %s", err))
	}
	s.Store = db

	// create some enterprise objects in the database, for testing purposes
	enterprises := []params.Enterprise{}
	for i := 1; i <= 3; i++ {
		enterprise, err := db.CreateEnterprise(
			context.Background(),
			fmt.Sprintf("test-enterprise-%d", i),
			fmt.Sprintf("test-creds-%d", i),
			fmt.Sprintf("test-webhook-secret-%d", i),
		)
		if err != nil {
			s.FailNow(fmt.Sprintf("failed to create database object (test-enterprise-%d)", i))
		}

		enterprises = append(enterprises, enterprise)
	}

	// create store with mocked sql connection
	sqlDB, sqlMock, err := sqlmock.New()
	if err != nil {
		s.FailNow(fmt.Sprintf("failed to run 'sqlmock.New()', got error: %v", err))
	}
	s.T().Cleanup(func() { sqlDB.Close() })
	mysqlConfig := mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}
	gormConfig := &gorm.Config{}
	if flag.Lookup("test.v").Value.String() == "false" {
		gormConfig.Logger = logger.Default.LogMode(logger.Silent)
	}
	gormConn, err := gorm.Open(mysql.New(mysqlConfig), gormConfig)
	if err != nil {
		s.FailNow(fmt.Sprintf("fail to open gorm connection: %v", err))
	}
	s.StoreSQLMocked = &sqlDatabase{
		conn: gormConn,
		cfg:  garmTesting.GetTestSqliteDBConfig(s.T()),
	}

	// setup test fixtures
	var maxRunners uint = 30
	var minIdleRunners uint = 10
	fixtures := &EnterpriseTestFixtures{
		Enterprises: enterprises,
		CreateEnterpriseParams: params.CreateEnterpriseParams{
			Name:            "new-test-enterprise",
			CredentialsName: "new-creds",
			WebhookSecret:   "new-webhook-secret",
		},
		CreatePoolParams: params.CreatePoolParams{
			ProviderName:   "test-provider",
			MaxRunners:     3,
			MinIdleRunners: 1,
			Enabled:        true,
			Image:          "test-image",
			Flavor:         "test-flavor",
			OSType:         "linux",
			OSArch:         "amd64",
			Tags:           []string{"self-hosted", "arm64", "linux"},
		},
		CreateInstanceParams: params.CreateInstanceParams{
			Name:   "test-instance-name",
			OSType: "linux",
		},
		UpdateRepoParams: params.UpdateRepositoryParams{
			CredentialsName: "test-update-creds",
			WebhookSecret:   "test-update-repo-webhook-secret",
		},
		UpdatePoolParams: params.UpdatePoolParams{
			MaxRunners:     &maxRunners,
			MinIdleRunners: &minIdleRunners,
			Image:          "test-update-image",
			Flavor:         "test-update-flavor",
		},
		SQLMock: sqlMock,
	}
	s.Fixtures = fixtures
}

func (s *EnterpriseTestSuite) TestCreateEnterprise() {
	// call tested function
	enterprise, err := s.Store.CreateEnterprise(
		context.Background(),
		s.Fixtures.CreateEnterpriseParams.Name,
		s.Fixtures.CreateEnterpriseParams.CredentialsName,
		s.Fixtures.CreateEnterpriseParams.WebhookSecret)

	// assertions
	s.Require().Nil(err)
	storeEnterprise, err := s.Store.GetEnterpriseByID(context.Background(), enterprise.ID)
	if err != nil {
		s.FailNow(fmt.Sprintf("failed to get enterprise by id: %v", err))
	}
	s.Require().Equal(storeEnterprise.Name, enterprise.Name)
	s.Require().Equal(storeEnterprise.CredentialsName, enterprise.CredentialsName)
	s.Require().Equal(storeEnterprise.WebhookSecret, enterprise.WebhookSecret)
}

func (s *EnterpriseTestSuite) TestCreateEnterpriseInvalidDBPassphrase() {
	cfg := garmTesting.GetTestSqliteDBConfig(s.T())
	conn, err := newDBConn(cfg)
	if err != nil {
		s.FailNow(fmt.Sprintf("failed to create db connection: %s", err))
	}
	// make sure we use a 'sqlDatabase' struct with a wrong 'cfg.Passphrase'
	cfg.Passphrase = "wrong-passphrase" // it must have a size different than 32
	sqlDB := &sqlDatabase{
		conn: conn,
		cfg:  cfg,
	}

	_, err = sqlDB.CreateEnterprise(
		context.Background(),
		s.Fixtures.CreateEnterpriseParams.Name,
		s.Fixtures.CreateEnterpriseParams.CredentialsName,
		s.Fixtures.CreateEnterpriseParams.WebhookSecret)

	s.Require().NotNil(err)
	s.Require().Equal("encoding secret: invalid passphrase length (expected length 32 characters)", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterpriseDBCreateErr() {
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `enterprises`")).
		WillReturnError(fmt.Errorf("creating enterprise mock error"))
	s.Fixtures.SQLMock.ExpectRollback()

	_, err := s.StoreSQLMocked.CreateEnterprise(
		context.Background(),
		s.Fixtures.CreateEnterpriseParams.Name,
		s.Fixtures.CreateEnterpriseParams.CredentialsName,
		s.Fixtures.CreateEnterpriseParams.WebhookSecret)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("creating enterprise: creating enterprise mock error", err.Error())
}

func (s *EnterpriseTestSuite) TestGetEnterprise() {
	enterprise, err := s.Store.GetEnterprise(context.Background(), s.Fixtures.Enterprises[0].Name)

	s.Require().Nil(err)
	s.Require().Equal(s.Fixtures.Enterprises[0].Name, enterprise.Name)
	s.Require().Equal(s.Fixtures.Enterprises[0].ID, enterprise.ID)
}

func (s *EnterpriseTestSuite) TestGetEnterpriseCaseInsensitive() {
	enterprise, err := s.Store.GetEnterprise(context.Background(), "TeSt-eNtErPriSe-1")

	s.Require().Nil(err)
	s.Require().Equal("test-enterprise-1", enterprise.Name)
}

func (s *EnterpriseTestSuite) TestGetEnterpriseNotFound() {
	_, err := s.Store.GetEnterprise(context.Background(), "dummy-name")

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: not found", err.Error())
}

func (s *EnterpriseTestSuite) TestGetEnterpriseDBDecryptingErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE name = ? COLLATE NOCASE AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].Name).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow(s.Fixtures.Enterprises[0].Name))

	_, err := s.StoreSQLMocked.GetEnterprise(context.Background(), s.Fixtures.Enterprises[0].Name)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: missing secret", err.Error())
}

func (s *EnterpriseTestSuite) TestListEnterprises() {
	enterprises, err := s.Store.ListEnterprises(context.Background())

	s.Require().Nil(err)
	garmTesting.EqualDBEntityByName(s.T(), s.Fixtures.Enterprises, enterprises)
}

func (s *EnterpriseTestSuite) TestListEnterprisesDBFetchErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE `enterprises`.`deleted_at` IS NULL")).
		WillReturnError(fmt.Errorf("fetching user from database mock error"))

	_, err := s.StoreSQLMocked.ListEnterprises(context.Background())

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprises: fetching user from database mock error", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterprise() {
	err := s.Store.DeleteEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.Require().Nil(err)
	_, err = s.Store.GetEnterpriseByID(context.Background(), s.Fixtures.Enterprises[0].ID)
	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: not found", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterpriseInvalidEnterpriseID() {
	err := s.Store.DeleteEnterprise(context.Background(), "dummy-enterprise-id")

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterpriseDBDeleteErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("DELETE FROM `enterprises`")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnError(fmt.Errorf("mocked delete enterprise error"))
	s.Fixtures.SQLMock.ExpectRollback()

	err := s.StoreSQLMocked.DeleteEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("deleting enterprise: mocked delete enterprise error", err.Error())
}

func (s *EnterpriseTestSuite) TestUpdateEnterprise() {
	enterprise, err := s.Store.UpdateEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.UpdateRepoParams)

	s.Require().Nil(err)
	s.Require().Equal(s.Fixtures.UpdateRepoParams.CredentialsName, enterprise.CredentialsName)
	s.Require().Equal(s.Fixtures.UpdateRepoParams.WebhookSecret, enterprise.WebhookSecret)
}

func (s *EnterpriseTestSuite) TestUpdateEnterpriseInvalidEnterpriseID() {
	_, err := s.Store.UpdateEnterprise(context.Background(), "dummy-enterprise-id", s.Fixtures.UpdateRepoParams)

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestUpdateEnterpriseDBEncryptErr() {
	s.StoreSQLMocked.cfg.Passphrase = "wrong-passphrase"

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))

	_, err := s.StoreSQLMocked.UpdateEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.UpdateRepoParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("encoding secret: invalid passphrase length (expected length 32 characters)", err.Error())
}

func (s *EnterpriseTestSuite) TestUpdateEnterpriseDBSaveErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(("UPDATE `enterprises` SET")).
		WillReturnError(fmt.Errorf("saving enterprise mock error"))
	s.Fixtures.SQLMock.ExpectRollback()

	_, err := s.StoreSQLMocked.UpdateEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.UpdateRepoParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("saving enterprise: saving enterprise mock error", err.Error())
}

func (s *EnterpriseTestSuite) TestUpdateEnterpriseDBDecryptingErr() {
	s.StoreSQLMocked.cfg.Passphrase = "wrong-passphrase"
	s.Fixtures.UpdateRepoParams.WebhookSecret = "webhook-secret"

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))

	_, err := s.StoreSQLMocked.UpdateEnterprise(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.UpdateRepoParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("encoding secret: invalid passphrase length (expected length 32 characters)", err.Error())
}

func (s *EnterpriseTestSuite) TestGetEnterpriseByID() {
	enterprise, err := s.Store.GetEnterpriseByID(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.Require().Nil(err)
	s.Require().Equal(s.Fixtures.Enterprises[0].ID, enterprise.ID)
}

func (s *EnterpriseTestSuite) TestGetEnterpriseByIDInvalidEnterpriseID() {
	_, err := s.Store.GetEnterpriseByID(context.Background(), "dummy-enterprise-id")

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestGetEnterpriseByIDDBDecryptingErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND `pools`.`deleted_at` IS NULL")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id"}).AddRow(s.Fixtures.Enterprises[0].ID))

	_, err := s.StoreSQLMocked.GetEnterpriseByID(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: missing secret", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePool() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.Require().Nil(err)

	enterprise, err := s.Store.GetEnterpriseByID(context.Background(), s.Fixtures.Enterprises[0].ID)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot get enterprise by ID: %v", err))
	}
	s.Require().Equal(1, len(enterprise.Pools))
	s.Require().Equal(pool.ID, enterprise.Pools[0].ID)
	s.Require().Equal(s.Fixtures.CreatePoolParams.ProviderName, enterprise.Pools[0].ProviderName)
	s.Require().Equal(s.Fixtures.CreatePoolParams.MaxRunners, enterprise.Pools[0].MaxRunners)
	s.Require().Equal(s.Fixtures.CreatePoolParams.MinIdleRunners, enterprise.Pools[0].MinIdleRunners)
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolMissingTags() {
	s.Fixtures.CreatePoolParams.Tags = []string{}

	_, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.Require().NotNil(err)
	s.Require().Equal("no tags specified", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolInvalidEnterpriseID() {
	_, err := s.Store.CreateEnterprisePool(context.Background(), "dummy-enterprise-id", s.Fixtures.CreatePoolParams)

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolDBCreateErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WillReturnError(fmt.Errorf("mocked creating pool error"))

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("creating pool: fetching pool: mocked creating pool error", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterpriseDBPoolAlreadyExistErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WithArgs(
			s.Fixtures.Enterprises[0].ID,
			s.Fixtures.CreatePoolParams.ProviderName,
			s.Fixtures.CreatePoolParams.Image,
			s.Fixtures.CreatePoolParams.Flavor).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id", "provider_name", "image", "flavor"}).
			AddRow(
				s.Fixtures.Enterprises[0].ID,
				s.Fixtures.CreatePoolParams.ProviderName,
				s.Fixtures.CreatePoolParams.Image,
				s.Fixtures.CreatePoolParams.Flavor))

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal(runnerErrors.NewConflictError("pool with the same image and flavor already exists on this provider"), err)
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolDBFetchTagErr() {
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WithArgs(
			s.Fixtures.Enterprises[0].ID,
			s.Fixtures.CreatePoolParams.ProviderName,
			s.Fixtures.CreatePoolParams.Image,
			s.Fixtures.CreatePoolParams.Flavor).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id"}))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tags` WHERE name = ? AND `tags`.`deleted_at` IS NULL ORDER BY `tags`.`id` LIMIT 1")).
		WillReturnError(fmt.Errorf("mocked fetching tag error"))

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("fetching tag: fetching tag from database: mocked fetching tag error", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolDBAddingPoolErr() {
	s.Fixtures.CreatePoolParams.Tags = []string{"linux"}

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WithArgs(
			s.Fixtures.Enterprises[0].ID,
			s.Fixtures.CreatePoolParams.ProviderName,
			s.Fixtures.CreatePoolParams.Image,
			s.Fixtures.CreatePoolParams.Flavor).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id"}))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tags` WHERE name = ? AND `tags`.`deleted_at` IS NULL ORDER BY `tags`.`id` LIMIT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"linux"}))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `tags`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `pools`")).
		WillReturnError(fmt.Errorf("mocked adding pool error"))
	s.Fixtures.SQLMock.ExpectRollback()

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("adding pool: mocked adding pool error", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolDBSaveTagErr() {
	s.Fixtures.CreatePoolParams.Tags = []string{"linux"}

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WithArgs(
			s.Fixtures.Enterprises[0].ID,
			s.Fixtures.CreatePoolParams.ProviderName,
			s.Fixtures.CreatePoolParams.Image,
			s.Fixtures.CreatePoolParams.Flavor).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id"}))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tags` WHERE name = ? AND `tags`.`deleted_at` IS NULL ORDER BY `tags`.`id` LIMIT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"linux"}))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `tags`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `pools`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("UPDATE `pools` SET")).
		WillReturnError(fmt.Errorf("mocked saving tag error"))
	s.Fixtures.SQLMock.ExpectRollback()

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("saving tag: mocked saving tag error", err.Error())
}

func (s *EnterpriseTestSuite) TestCreateEnterprisePoolDBFetchPoolErr() {
	s.Fixtures.CreatePoolParams.Tags = []string{"linux"}

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `enterprises` WHERE id = ? AND `enterprises`.`deleted_at` IS NULL ORDER BY `enterprises`.`id` LIMIT 1")).
		WithArgs(s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(s.Fixtures.Enterprises[0].ID))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE `pools`.`enterprise_id` = ? AND (provider_name = ? and image = ? and flavor = ?) AND `pools`.`deleted_at` IS NULL")).
		WithArgs(
			s.Fixtures.Enterprises[0].ID,
			s.Fixtures.CreatePoolParams.ProviderName,
			s.Fixtures.CreatePoolParams.Image,
			s.Fixtures.CreatePoolParams.Flavor).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id"}))
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tags` WHERE name = ? AND `tags`.`deleted_at` IS NULL ORDER BY `tags`.`id` LIMIT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"linux"}))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `tags`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `pools`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("UPDATE `pools` SET")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `tags`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("INSERT INTO `pool_tags`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.Fixtures.SQLMock.ExpectCommit()
	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE id = ? AND `pools`.`deleted_at` IS NULL ORDER BY `pools`.`id` LIMIT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := s.StoreSQLMocked.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("fetching pool: not found", err.Error())
}

func (s *EnterpriseTestSuite) TestListEnterprisePools() {
	enterprisePools := []params.Pool{}
	for i := 1; i <= 2; i++ {
		s.Fixtures.CreatePoolParams.Flavor = fmt.Sprintf("test-flavor-%v", i)
		pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
		if err != nil {
			s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
		}
		enterprisePools = append(enterprisePools, pool)
	}

	pools, err := s.Store.ListEnterprisePools(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.Require().Nil(err)
	garmTesting.EqualDBEntityID(s.T(), enterprisePools, pools)
}

func (s *EnterpriseTestSuite) TestListEnterprisePoolsInvalidEnterpriseID() {
	_, err := s.Store.ListEnterprisePools(context.Background(), "dummy-enterprise-id")

	s.Require().NotNil(err)
	s.Require().Equal("fetching pools: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestGetEnterprisePool() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}

	enterprisePool, err := s.Store.GetEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, pool.ID)

	s.Require().Nil(err)
	s.Require().Equal(enterprisePool.ID, pool.ID)
}

func (s *EnterpriseTestSuite) TestGetEnterprisePoolInvalidEnterpriseID() {
	_, err := s.Store.GetEnterprisePool(context.Background(), "dummy-enterprise-id", "dummy-pool-id")

	s.Require().NotNil(err)
	s.Require().Equal("fetching pool: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterprisePool() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}

	err = s.Store.DeleteEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, pool.ID)

	s.Require().Nil(err)
	_, err = s.Store.GetEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, pool.ID)
	s.Require().Equal("fetching pool: finding pool: not found", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterprisePoolInvalidEnterpriseID() {
	err := s.Store.DeleteEnterprisePool(context.Background(), "dummy-enterprise-id", "dummy-pool-id")

	s.Require().NotNil(err)
	s.Require().Equal("looking up enterprise pool: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestDeleteEnterprisePoolDBDeleteErr() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}

	s.Fixtures.SQLMock.
		ExpectQuery(regexp.QuoteMeta("SELECT * FROM `pools` WHERE (id = ? and enterprise_id = ?) AND `pools`.`deleted_at` IS NULL ORDER BY `pools`.`id` LIMIT 1")).
		WithArgs(pool.ID, s.Fixtures.Enterprises[0].ID).
		WillReturnRows(sqlmock.NewRows([]string{"enterprise_id", "id"}).AddRow(s.Fixtures.Enterprises[0].ID, pool.ID))
	s.Fixtures.SQLMock.ExpectBegin()
	s.Fixtures.SQLMock.
		ExpectExec(regexp.QuoteMeta("DELETE FROM `pools` WHERE `pools`.`id` = ?")).
		WithArgs(pool.ID).
		WillReturnError(fmt.Errorf("mocked deleting pool error"))
	s.Fixtures.SQLMock.ExpectRollback()

	err = s.StoreSQLMocked.DeleteEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, pool.ID)

	s.assertSQLMockExpectations()
	s.Require().NotNil(err)
	s.Require().Equal("deleting pool: mocked deleting pool error", err.Error())
}

func (s *EnterpriseTestSuite) TestFindEnterprisePoolByTags() {
	enterprisePool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}

	pool, err := s.Store.FindEnterprisePoolByTags(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams.Tags)

	s.Require().Nil(err)
	s.Require().Equal(enterprisePool.ID, pool.ID)
	s.Require().Equal(enterprisePool.Image, pool.Image)
	s.Require().Equal(enterprisePool.Flavor, pool.Flavor)
}

func (s *EnterpriseTestSuite) TestFindEnterprisePoolByTagsMissingTags() {
	tags := []string{}

	_, err := s.Store.FindEnterprisePoolByTags(context.Background(), s.Fixtures.Enterprises[0].ID, tags)

	s.Require().NotNil(err)
	s.Require().Equal("fetching pool: missing tags", err.Error())
}

func (s *EnterpriseTestSuite) TestListEnterpriseInstances() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}
	poolInstances := []params.Instance{}
	for i := 1; i <= 3; i++ {
		s.Fixtures.CreateInstanceParams.Name = fmt.Sprintf("test-enterprise-%v", i)
		instance, err := s.Store.CreateInstance(context.Background(), pool.ID, s.Fixtures.CreateInstanceParams)
		if err != nil {
			s.FailNow(fmt.Sprintf("cannot create instance: %s", err))
		}
		poolInstances = append(poolInstances, instance)
	}

	instances, err := s.Store.ListEnterpriseInstances(context.Background(), s.Fixtures.Enterprises[0].ID)

	s.Require().Nil(err)
	s.equalInstancesByName(poolInstances, instances)
}

func (s *EnterpriseTestSuite) TestListEnterpriseInstancesInvalidEnterpriseID() {
	_, err := s.Store.ListEnterpriseInstances(context.Background(), "dummy-enterprise-id")

	s.Require().NotNil(err)
	s.Require().Equal("fetching enterprise: parsing id: invalid request", err.Error())
}

func (s *EnterpriseTestSuite) TestUpdateEnterprisePool() {
	pool, err := s.Store.CreateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, s.Fixtures.CreatePoolParams)
	if err != nil {
		s.FailNow(fmt.Sprintf("cannot create enterprise pool: %v", err))
	}

	pool, err = s.Store.UpdateEnterprisePool(context.Background(), s.Fixtures.Enterprises[0].ID, pool.ID, s.Fixtures.UpdatePoolParams)

	s.Require().Nil(err)
	s.Require().Equal(*s.Fixtures.UpdatePoolParams.MaxRunners, pool.MaxRunners)
	s.Require().Equal(*s.Fixtures.UpdatePoolParams.MinIdleRunners, pool.MinIdleRunners)
	s.Require().Equal(s.Fixtures.UpdatePoolParams.Image, pool.Image)
	s.Require().Equal(s.Fixtures.UpdatePoolParams.Flavor, pool.Flavor)
}

func (s *EnterpriseTestSuite) TestUpdateEnterprisePoolInvalidEnterpriseID() {
	_, err := s.Store.UpdateEnterprisePool(context.Background(), "dummy-enterprise-id", "dummy-pool-id", s.Fixtures.UpdatePoolParams)

	s.Require().NotNil(err)
	s.Require().Equal("fetching pool: parsing id: invalid request", err.Error())
}

func TestEnterpriseTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(EnterpriseTestSuite))
}
