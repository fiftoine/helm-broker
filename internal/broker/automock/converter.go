// Code generated by mockery v1.0.0. DO NOT EDIT.

package automock

import internal "github.com/kyma-project/helm-broker/internal"
import mock "github.com/stretchr/testify/mock"
import v2 "github.com/pmorie/go-open-service-broker-client/v2"

// converter is an autogenerated mock type for the converter type
type converter struct {
	mock.Mock
}

// Convert provides a mock function with given fields: b
func (_m *converter) Convert(b *internal.Addon) (v2.Service, error) {
	ret := _m.Called(b)

	var r0 v2.Service
	if rf, ok := ret.Get(0).(func(*internal.Addon) v2.Service); ok {
		r0 = rf(b)
	} else {
		r0 = ret.Get(0).(v2.Service)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*internal.Addon) error); ok {
		r1 = rf(b)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
