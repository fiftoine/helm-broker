package broker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/kyma-project/helm-broker/internal"
	"github.com/pkg/errors"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/sirupsen/logrus"
	helmErrors "k8s.io/helm/pkg/storage/errors"
)

type deprovisionService struct {
	instanceGetter          instanceGetter
	instanceRemover         instanceRemover
	instanceStateGetter     instanceStateDeprovisionGetter
	operationInserter       operationInserter
	operationUpdater        operationUpdater
	instanceBindDataRemover instanceBindDataRemover
	operationIDProvider     func() (internal.OperationID, error)
	helmDeleter             helmDeleter

	mu  sync.Mutex
	log logrus.FieldLogger

	testHookAsyncCalled func(internal.OperationID)
}

func (svc *deprovisionService) Deprovision(ctx context.Context, osbCtx OsbContext, req *osb.DeprovisionRequest) (*osb.DeprovisionResponse, error) {
	if !req.AcceptsIncomplete {
		return nil, errors.New("asynchronous operation mode required")
	}

	// Single deprovisioning is supported concurrently.
	// TODO: switch to lock per instanceID
	svc.mu.Lock()
	defer svc.mu.Unlock()

	iID := internal.InstanceID(req.InstanceID)

	switch state, err := svc.instanceStateGetter.IsDeprovisioned(iID); true {
	case IsNotFoundError(err):
		return nil, err
	case err != nil:
		return nil, errors.Wrap(err, "while checking if instance is already deprovisioned")
	case state:
		return &osb.DeprovisionResponse{Async: false}, nil
	}

	switch opIDInProgress, inProgress, err := svc.instanceStateGetter.IsDeprovisioningInProgress(iID); true {
	case IsNotFoundError(err):
		return nil, err
	case err != nil:
		return nil, errors.Wrap(err, "while checking if instance is being deprovisioned")
	case inProgress:
		opKeyInProgress := osb.OperationKey(opIDInProgress)
		return &osb.DeprovisionResponse{Async: true, OperationKey: &opKeyInProgress}, nil
	}

	id, err := svc.operationIDProvider()
	if err != nil {
		return nil, errors.Wrap(err, "while generating ID for operation")
	}
	opID := internal.OperationID(id)

	i, err := svc.instanceGetter.Get(iID)
	switch {
	case IsNotFoundError(err):
		return nil, err
	case err != nil:
		return nil, errors.Wrap(err, "while getting instance")
	}

	// TODO: check if svcID/planID from request are matching the one from instance
	//svcID := internal.ServiceID(req.ServiceID)
	//svcPlanID := internal.ServicePlanID(req.PlanID)

	op := internal.InstanceOperation{
		InstanceID:  iID,
		OperationID: opID,
		Type:        internal.OperationTypeRemove,
		State:       internal.OperationStateInProgress,
		ProvisioningParameters: &internal.RequestParameters{
			Data: make(map[string]interface{}),
		},
	}

	if err := svc.operationInserter.Insert(&op); err != nil {
		return nil, errors.Wrap(err, "while inserting instance operation to storage")
	}

	svc.doAsync(ctx, iID, opID, i.ReleaseName)

	opKey := osb.OperationKey(op.OperationID)
	resp := &osb.DeprovisionResponse{
		OperationKey: &opKey,
		Async:        true,
	}

	return resp, nil
}

func (svc *deprovisionService) doAsync(ctx context.Context, iID internal.InstanceID, opID internal.OperationID, releaseName internal.ReleaseName) {
	if svc.testHookAsyncCalled != nil {
		svc.testHookAsyncCalled(opID)
	}
	go svc.do(ctx, iID, opID, releaseName)
}

// do is called asynchronously
func (svc *deprovisionService) do(ctx context.Context, iID internal.InstanceID, opID internal.OperationID, releaseName internal.ReleaseName) {

	fDo := func() error {
		err := svc.helmDeleter.Delete(releaseName)
		if err != nil && !isErrReleaseNotFound(err, releaseName) {
			return errors.Wrapf(err, "while deleting helm release %q", releaseName)
		}

		err = svc.instanceBindDataRemover.Remove(iID)
		switch {
		// we are not checking if instance was bindable and because of that NotFound error is also in happy path
		// BEWARE: such solution can produce false positive errors e.g.
		// 1. We are executing remove of data even if instance is not bindable (no data are stored)
		// 2. We are getting error on connection to storage, so notFound error cannot be returned
		// 3. Then deprovisioning is wrongly marked as failed
		case err == nil, IsNotFoundError(err):
		default:
			return errors.Wrap(err, "while removing instance bind data from storage")
		}

		// remove instance entity from storage
		err = svc.instanceRemover.Remove(iID)
		switch {
		case err == nil, IsNotFoundError(err):
		default:
			return errors.Wrap(err, "while removing instance entity from storage")
		}

		return nil
	}

	opState := internal.OperationStateSucceeded
	opDesc := "deprovisioning succeeded"
	if err := fDo(); err != nil {
		opState = internal.OperationStateFailed
		opDesc = fmt.Sprintf("deprovisioning failed on error: %s", err.Error())
	}

	if err := svc.operationUpdater.UpdateStateDesc(iID, opID, opState, &opDesc); err != nil {
		svc.log.Errorf("Cannot update state for instance [%s]: [%v]", iID, err)
		return
	}
}

// isErrReleaseNotFound implements the error checking for Helm Releases, copied from
// https://github.com/helm/helm/blob/HEAD@%7B2019-05-30T10:19:27Z%7D/cmd/helm/upgrade.go#L222
func isErrReleaseNotFound(err error, releaseName internal.ReleaseName) bool {
	return strings.Contains(err.Error(), helmErrors.ErrReleaseNotFound(string(releaseName)).Error())
}
