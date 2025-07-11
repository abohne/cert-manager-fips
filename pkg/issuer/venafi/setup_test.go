/*
Copyright 2020 The cert-manager Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package venafi

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/go-logr/logr"

	internalinformers "github.com/cert-manager/cert-manager/internal/informers"
	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	controllerpkg "github.com/cert-manager/cert-manager/pkg/controller"
	controllertest "github.com/cert-manager/cert-manager/pkg/controller/test"
	"github.com/cert-manager/cert-manager/pkg/issuer/venafi/client"
	internalvenafifake "github.com/cert-manager/cert-manager/pkg/issuer/venafi/client/fake"
	logf "github.com/cert-manager/cert-manager/pkg/logs"
	"github.com/cert-manager/cert-manager/pkg/metrics"
	"github.com/cert-manager/cert-manager/test/unit/gen"
)

func TestSetup(t *testing.T) {
	baseIssuer := gen.Issuer("test-issuer")

	failingClientBuilder := func(string, internalinformers.SecretLister,
		cmapi.GenericIssuer, *metrics.Metrics, logr.Logger, string) (client.Interface, error) {
		return nil, errors.New("this is an error")
	}

	failingPingClient := func(string, internalinformers.SecretLister,
		cmapi.GenericIssuer, *metrics.Metrics, logr.Logger, string) (client.Interface, error) {
		return &internalvenafifake.Venafi{
			PingFn: func() error {
				return errors.New("this is a ping error")
			},
		}, nil
	}

	pingClient := func(string, internalinformers.SecretLister,
		cmapi.GenericIssuer, *metrics.Metrics, logr.Logger, string) (client.Interface, error) {
		return &internalvenafifake.Venafi{
			PingFn: func() error {
				return nil
			},
		}, nil
	}

	verifyCredentialsClient := func(string, internalinformers.SecretLister, cmapi.GenericIssuer, *metrics.Metrics, logr.Logger, string) (client.Interface, error) {
		return &internalvenafifake.Venafi{
			PingFn: func() error {
				return nil
			},
			VerifyCredentialsFn: func() error {
				return nil
			},
		}, nil
	}

	failingVerifyCredentialsClient := func(string, internalinformers.SecretLister, cmapi.GenericIssuer, *metrics.Metrics, logr.Logger, string) (client.Interface, error) {
		return &internalvenafifake.Venafi{
			PingFn: func() error {
				return nil
			},
			VerifyCredentialsFn: func() error {
				return fmt.Errorf("401 Unauthorized")
			},
		}, nil
	}

	tests := map[string]testSetupT{
		"if client builder fails then should error": {
			clientBuilder: failingClientBuilder,
			expectedErr:   true,
			iss:           baseIssuer.DeepCopy(),
			expectedCondition: &cmapi.IssuerCondition{
				Reason:  "ErrorSetup",
				Message: "Failed to setup Venafi issuer: error building client: this is an error",
				Status:  "False",
			},
		},

		"if ping fails then should error": {
			clientBuilder: failingPingClient,
			iss:           baseIssuer.DeepCopy(),
			expectedErr:   true,
			expectedCondition: &cmapi.IssuerCondition{
				Reason:  "ErrorSetup",
				Message: "Failed to setup Venafi issuer: error pinging Venafi API: this is a ping error",
				Status:  "False",
			},
		},

		"if ready then should set condition": {
			clientBuilder: pingClient,
			iss:           baseIssuer.DeepCopy(),
			expectedErr:   false,
			expectedCondition: &cmapi.IssuerCondition{
				Message: "Venafi issuer started",
				Reason:  "Venafi issuer started",
				Status:  "True",
			},
			expectedEvents: []string{
				"Normal Ready Verified issuer with Venafi server",
			},
		},
		"verifyCredentials happy path": {
			clientBuilder: verifyCredentialsClient,
			iss:           baseIssuer.DeepCopy(),
			expectedErr:   false,
			expectedCondition: &cmapi.IssuerCondition{
				Message: "Venafi issuer started",
				Reason:  "Venafi issuer started",
				Status:  "True",
			},
			expectedEvents: []string{
				"Normal Ready Verified issuer with Venafi server",
			},
		},

		"if verifyCredentials returns an error we should set condition to False": {
			clientBuilder: failingVerifyCredentialsClient,
			iss:           baseIssuer.DeepCopy(),
			expectedErr:   true,
			expectedCondition: &cmapi.IssuerCondition{
				Reason:  "ErrorSetup",
				Message: "Failed to setup Venafi issuer: client.VerifyCredentials: 401 Unauthorized",
				Status:  "False",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			test.runTest(t)
		})
	}
}

type testSetupT struct {
	clientBuilder client.VenafiClientBuilder
	iss           cmapi.GenericIssuer

	expectedErr       bool
	expectedEvents    []string
	expectedCondition *cmapi.IssuerCondition
}

func (s *testSetupT) runTest(t *testing.T) {
	rec := &controllertest.FakeRecorder{}

	v := &Venafi{
		Context: &controllerpkg.Context{
			Recorder: rec,
		},
		clientBuilder: s.clientBuilder,
		log:           logf.Log.WithName("venafi"),
	}

	err := v.Setup(t.Context(), s.iss)
	if err != nil && !s.expectedErr {
		t.Errorf("expected to not get an error, but got: %v", err)
	}
	if err == nil && s.expectedErr {
		t.Errorf("expected to get an error but did not get one")
	}

	if !slices.Equal(s.expectedEvents, rec.Events) {
		t.Errorf("got unexpected events, exp='%s' got='%s'",
			s.expectedEvents, rec.Events)
	}

	conditions := s.iss.GetStatus().Conditions
	if s.expectedCondition == nil &&
		len(conditions) > 0 {
		t.Errorf("expected no conditions but got=%+v",
			conditions)
	}

	if s.expectedCondition != nil {
		if len(conditions) != 1 {
			t.Error("expected conditions but got none")
			t.FailNow()
		}

		c := conditions[0]

		if s.expectedCondition.Message != c.Message {
			t.Errorf("unexpected condition message, exp=%s got=%s",
				s.expectedCondition.Message, c.Message)
		}
		if s.expectedCondition.Reason != c.Reason {
			t.Errorf("unexpected condition reason, exp=%s got=%s",
				s.expectedCondition.Reason, c.Reason)
		}
		if s.expectedCondition.Status != c.Status {
			t.Errorf("unexpected condition status, exp=%s got=%s",
				s.expectedCondition.Status, c.Status)
		}
	}
}
