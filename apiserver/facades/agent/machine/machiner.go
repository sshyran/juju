// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// The machiner package implements the API interface
// used by the machiner worker.
package machine

import (
	"github.com/juju/loggo"
	"github.com/juju/names/v4"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/common/networkingcommon"
	apiservererrors "github.com/juju/juju/apiserver/errors"
	"github.com/juju/juju/apiserver/facade"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/core/model"
	"github.com/juju/juju/state"
)

var logger = loggo.GetLogger("juju.apiserver.machine")

// MachinerAPI implements the API used by the machiner worker.
type MachinerAPI struct {
	*common.LifeGetter
	*common.StatusSetter
	*common.DeadEnsurer
	*common.AgentEntityWatcher
	*common.APIAddresser
	*networkingcommon.NetworkConfigAPI

	st           *state.State
	auth         facade.Authorizer
	getCanModify common.GetAuthFunc
	getCanRead   common.GetAuthFunc
}

// NewMachinerAPI creates a new instance of the Machiner API.
func NewMachinerAPI(st *state.State, resources facade.Resources, authorizer facade.Authorizer) (*MachinerAPI, error) {
	if !authorizer.AuthMachineAgent() {
		return nil, apiservererrors.ErrPerm
	}
	getCanModify := func() (common.AuthFunc, error) {
		return authorizer.AuthOwner, nil
	}
	getCanRead := func() (common.AuthFunc, error) {
		return authorizer.AuthOwner, nil
	}
	return &MachinerAPI{
		LifeGetter:         common.NewLifeGetter(st, getCanRead),
		StatusSetter:       common.NewStatusSetter(st, getCanModify),
		DeadEnsurer:        common.NewDeadEnsurer(st, nil, getCanModify),
		AgentEntityWatcher: common.NewAgentEntityWatcher(st, resources, getCanRead),
		APIAddresser:       common.NewAPIAddresser(st, resources),
		NetworkConfigAPI:   networkingcommon.NewNetworkConfigAPI(st, getCanModify),
		st:                 st,
		auth:               authorizer,
		getCanModify:       getCanModify,
		getCanRead:         getCanRead,
	}, nil
}

func (api *MachinerAPI) getMachine(tag string, authChecker common.AuthFunc) (*state.Machine, error) {
	mtag, err := names.ParseMachineTag(tag)
	if err != nil {
		return nil, apiservererrors.ErrPerm
	} else if !authChecker(mtag) {
		return nil, apiservererrors.ErrPerm
	}

	entity, err := api.st.FindEntity(mtag)
	if err != nil {
		return nil, err
	}
	return entity.(*state.Machine), nil
}

func (api *MachinerAPI) SetMachineAddresses(args params.SetMachinesAddresses) (params.ErrorResults, error) {
	results := params.ErrorResults{
		Results: make([]params.ErrorResult, len(args.MachineAddresses)),
	}
	canModify, err := api.getCanModify()
	if err != nil {
		return results, err
	}
	for i, arg := range args.MachineAddresses {
		m, err := api.getMachine(arg.Tag, canModify)
		if err != nil {
			results.Results[i].Error = apiservererrors.ServerError(err)
			continue
		}
		addresses, err := params.ToProviderAddresses(arg.Addresses...).ToSpaceAddresses(api.st)
		if err != nil {
			results.Results[i].Error = apiservererrors.ServerError(err)
			continue
		}
		if err := m.SetMachineAddresses(addresses...); err != nil {
			results.Results[i].Error = apiservererrors.ServerError(err)
		}
	}
	return results, nil
}

// Jobs returns the jobs assigned to the given entities.
func (api *MachinerAPI) Jobs(args params.Entities) (params.JobsResults, error) {
	result := params.JobsResults{
		Results: make([]params.JobsResult, len(args.Entities)),
	}

	canRead, err := api.getCanRead()
	if err != nil {
		return result, err
	}

	for i, agent := range args.Entities {
		machine, err := api.getMachine(agent.Tag, canRead)
		if err != nil {
			result.Results[i].Error = apiservererrors.ServerError(err)
			continue
		}
		machineJobs := machine.Jobs()
		jobs := make([]model.MachineJob, len(machineJobs))
		for i, job := range machineJobs {
			jobs[i] = job.ToParams()
		}
		result.Results[i].Jobs = jobs
	}
	return result, nil
}

// RecordAgentStartTime updates the agent start time field in the machine doc.
func (api *MachinerAPI) RecordAgentStartTime(args params.Entities) (params.ErrorResults, error) {
	results := params.ErrorResults{
		Results: make([]params.ErrorResult, len(args.Entities)),
	}
	canModify, err := api.getCanModify()
	if err != nil {
		return results, err
	}

	for i, entity := range args.Entities {
		m, err := api.getMachine(entity.Tag, canModify)
		if err != nil {
			results.Results[i].Error = apiservererrors.ServerError(err)
			continue
		}
		if err := m.RecordAgentStartTime(); err != nil {
			results.Results[i].Error = apiservererrors.ServerError(err)
		}
	}
	return results, nil
}

// MachinerAPIV1 implements the V1 API used by the machiner worker.
type MachinerAPIV1 struct {
	*MachinerAPIV2
}

// MachinerAPIV2 implements the V2 API used by the machiner worker.
// It adds RecordAgentStartTime and back-fills the missing origin in
// NetworkConfig.
type MachinerAPIV2 struct {
	*MachinerAPIV3
}

// MachinerAPIV3 implements the V3 API used by the machiner worker.
// It removes SetProviderNetworkConfig.
type MachinerAPIV3 struct {
	*MachinerAPI
}

// NewMachinerAPIV1 creates a new instance of the V1 Machiner API.
func NewMachinerAPIV1(
	st *state.State, resources facade.Resources, authorizer facade.Authorizer,
) (*MachinerAPIV1, error) {
	api, err := NewMachinerAPIV2(st, resources, authorizer)
	if err != nil {
		return nil, err
	}

	return &MachinerAPIV1{api}, nil
}

// NewMachinerAPIV2 creates a new instance of the V2 Machiner API.
func NewMachinerAPIV2(
	st *state.State, resources facade.Resources, authorizer facade.Authorizer,
) (*MachinerAPIV2, error) {
	api, err := NewMachinerAPIV3(st, resources, authorizer)
	if err != nil {
		return nil, err
	}

	return &MachinerAPIV2{api}, nil
}

// SetObservedNetworkConfig back-fills machine origin before calling through to
// the networking common method of the same name.
// Older agents do not set the origin, so we must do it for them.
func (api *MachinerAPIV2) SetObservedNetworkConfig(args params.SetMachineNetworkConfig) error {
	args.BackFillMachineOrigin()
	return api.NetworkConfigAPI.SetObservedNetworkConfig(args)
}

// NewMachinerAPIV3 creates a new instance of the V3 Machiner API.
func NewMachinerAPIV3(
	st *state.State, resources facade.Resources, authorizer facade.Authorizer,
) (*MachinerAPIV3, error) {
	api, err := NewMachinerAPI(st, resources, authorizer)
	if err != nil {
		return nil, err
	}

	return &MachinerAPIV3{api}, nil
}

// SetProviderNetworkConfig is no-op.
// This method stub is here, because the method was removed from the common
// networking API.
// It was only ever called by controller machine agents during start-up.
// Not only was this unnecessary, it duplicated link-layer devices on AWS.
func (api *MachinerAPIV3) SetProviderNetworkConfig(args params.Entities) (params.ErrorResults, error) {
	return params.ErrorResults{
		Results: make([]params.ErrorResult, len(args.Entities)),
	}, nil
}

// RecordAgentStartTime is not available in V1.
func (api *MachinerAPIV1) RecordAgentStartTime(_, _ struct{}) {}
