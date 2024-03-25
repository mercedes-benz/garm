// Code generated by mockery v2.42.0. DO NOT EDIT.

package mocks

import (
	context "context"

	github "github.com/google/go-github/v57/github"
	mock "github.com/stretchr/testify/mock"

	params "github.com/cloudbase/garm/params"
)

// GithubEntityOperations is an autogenerated mock type for the GithubEntityOperations type
type GithubEntityOperations struct {
	mock.Mock
}

// CreateEntityHook provides a mock function with given fields: ctx, hook
func (_m *GithubEntityOperations) CreateEntityHook(ctx context.Context, hook *github.Hook) (*github.Hook, error) {
	ret := _m.Called(ctx, hook)

	if len(ret) == 0 {
		panic("no return value specified for CreateEntityHook")
	}

	var r0 *github.Hook
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *github.Hook) (*github.Hook, error)); ok {
		return rf(ctx, hook)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *github.Hook) *github.Hook); ok {
		r0 = rf(ctx, hook)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Hook)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *github.Hook) error); ok {
		r1 = rf(ctx, hook)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// CreateEntityRegistrationToken provides a mock function with given fields: ctx
func (_m *GithubEntityOperations) CreateEntityRegistrationToken(ctx context.Context) (*github.RegistrationToken, *github.Response, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for CreateEntityRegistrationToken")
	}

	var r0 *github.RegistrationToken
	var r1 *github.Response
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context) (*github.RegistrationToken, *github.Response, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *github.RegistrationToken); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.RegistrationToken)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) *github.Response); ok {
		r1 = rf(ctx)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*github.Response)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context) error); ok {
		r2 = rf(ctx)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// DeleteEntityHook provides a mock function with given fields: ctx, id
func (_m *GithubEntityOperations) DeleteEntityHook(ctx context.Context, id int64) (*github.Response, error) {
	ret := _m.Called(ctx, id)

	if len(ret) == 0 {
		panic("no return value specified for DeleteEntityHook")
	}

	var r0 *github.Response
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64) (*github.Response, error)); ok {
		return rf(ctx, id)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64) *github.Response); ok {
		r0 = rf(ctx, id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Response)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetEntityHook provides a mock function with given fields: ctx, id
func (_m *GithubEntityOperations) GetEntityHook(ctx context.Context, id int64) (*github.Hook, error) {
	ret := _m.Called(ctx, id)

	if len(ret) == 0 {
		panic("no return value specified for GetEntityHook")
	}

	var r0 *github.Hook
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64) (*github.Hook, error)); ok {
		return rf(ctx, id)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64) *github.Hook); ok {
		r0 = rf(ctx, id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Hook)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetEntityJITConfig provides a mock function with given fields: ctx, instance, pool, labels
func (_m *GithubEntityOperations) GetEntityJITConfig(ctx context.Context, instance string, pool params.Pool, labels []string) (map[string]string, *github.Runner, error) {
	ret := _m.Called(ctx, instance, pool, labels)

	if len(ret) == 0 {
		panic("no return value specified for GetEntityJITConfig")
	}

	var r0 map[string]string
	var r1 *github.Runner
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, string, params.Pool, []string) (map[string]string, *github.Runner, error)); ok {
		return rf(ctx, instance, pool, labels)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, params.Pool, []string) map[string]string); ok {
		r0 = rf(ctx, instance, pool, labels)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[string]string)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, params.Pool, []string) *github.Runner); ok {
		r1 = rf(ctx, instance, pool, labels)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*github.Runner)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, string, params.Pool, []string) error); ok {
		r2 = rf(ctx, instance, pool, labels)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// ListEntityHooks provides a mock function with given fields: ctx, opts
func (_m *GithubEntityOperations) ListEntityHooks(ctx context.Context, opts *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	ret := _m.Called(ctx, opts)

	if len(ret) == 0 {
		panic("no return value specified for ListEntityHooks")
	}

	var r0 []*github.Hook
	var r1 *github.Response
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, *github.ListOptions) ([]*github.Hook, *github.Response, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *github.ListOptions) []*github.Hook); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*github.Hook)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *github.ListOptions) *github.Response); ok {
		r1 = rf(ctx, opts)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*github.Response)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, *github.ListOptions) error); ok {
		r2 = rf(ctx, opts)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// ListEntityRunnerApplicationDownloads provides a mock function with given fields: ctx
func (_m *GithubEntityOperations) ListEntityRunnerApplicationDownloads(ctx context.Context) ([]*github.RunnerApplicationDownload, *github.Response, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for ListEntityRunnerApplicationDownloads")
	}

	var r0 []*github.RunnerApplicationDownload
	var r1 *github.Response
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context) ([]*github.RunnerApplicationDownload, *github.Response, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) []*github.RunnerApplicationDownload); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*github.RunnerApplicationDownload)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) *github.Response); ok {
		r1 = rf(ctx)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*github.Response)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context) error); ok {
		r2 = rf(ctx)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// ListEntityRunners provides a mock function with given fields: ctx, opts
func (_m *GithubEntityOperations) ListEntityRunners(ctx context.Context, opts *github.ListOptions) (*github.Runners, *github.Response, error) {
	ret := _m.Called(ctx, opts)

	if len(ret) == 0 {
		panic("no return value specified for ListEntityRunners")
	}

	var r0 *github.Runners
	var r1 *github.Response
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, *github.ListOptions) (*github.Runners, *github.Response, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *github.ListOptions) *github.Runners); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Runners)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *github.ListOptions) *github.Response); ok {
		r1 = rf(ctx, opts)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*github.Response)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, *github.ListOptions) error); ok {
		r2 = rf(ctx, opts)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// PingEntityHook provides a mock function with given fields: ctx, id
func (_m *GithubEntityOperations) PingEntityHook(ctx context.Context, id int64) (*github.Response, error) {
	ret := _m.Called(ctx, id)

	if len(ret) == 0 {
		panic("no return value specified for PingEntityHook")
	}

	var r0 *github.Response
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64) (*github.Response, error)); ok {
		return rf(ctx, id)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64) *github.Response); ok {
		r0 = rf(ctx, id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Response)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RemoveEntityRunner provides a mock function with given fields: ctx, runnerID
func (_m *GithubEntityOperations) RemoveEntityRunner(ctx context.Context, runnerID int64) (*github.Response, error) {
	ret := _m.Called(ctx, runnerID)

	if len(ret) == 0 {
		panic("no return value specified for RemoveEntityRunner")
	}

	var r0 *github.Response
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64) (*github.Response, error)); ok {
		return rf(ctx, runnerID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64) *github.Response); ok {
		r0 = rf(ctx, runnerID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*github.Response)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, runnerID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NewGithubEntityOperations creates a new instance of GithubEntityOperations. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewGithubEntityOperations(t interface {
	mock.TestingT
	Cleanup(func())
}) *GithubEntityOperations {
	mock := &GithubEntityOperations{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}