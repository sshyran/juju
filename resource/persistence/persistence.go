// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package persistence

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/names"
	jujutxn "github.com/juju/txn"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/resource"
)

var logger = loggo.GetLogger("juju.resource.persistence")

// PersistenceBase exposes the core persistence functionality needed
// for resources.
type PersistenceBase interface {
	// All populates docs with the list of the documents corresponding
	// to the provided query.
	All(collName string, query, docs interface{}) error

	// Run runs the transaction generated by the provided factory
	// function. It may be retried several times.
	Run(transactions jujutxn.TransactionSource) error
}

// Persistence provides the persistence functionality for the
// Juju environment as a whole.
type Persistence struct {
	base PersistenceBase
}

// NewPersistence wraps the base in a new Persistence.
func NewPersistence(base PersistenceBase) *Persistence {
	return &Persistence{
		base: base,
	}
}

// ListResources returns the info for each non-pending resource of the
// identified service.
func (p Persistence) ListResources(serviceID string) (resource.ServiceResources, error) {
	logger.Tracef("listing all resources for service %q", serviceID)

	// TODO(ericsnow) Ensure that the service is still there?

	docs, err := p.resources(serviceID)
	if err != nil {
		return resource.ServiceResources{}, errors.Trace(err)
	}

	units := map[names.UnitTag][]resource.Resource{}

	var results resource.ServiceResources
	for _, doc := range docs {
		if doc.PendingID != "" {
			continue
		}

		res, err := doc2basicResource(doc)
		if err != nil {
			return resource.ServiceResources{}, errors.Trace(err)
		}
		if doc.UnitID == "" {
			results.Resources = append(results.Resources, res)
			continue
		}
		tag := names.NewUnitTag(doc.UnitID)
		units[tag] = append(units[tag], res)
	}
	for tag, res := range units {
		results.UnitResources = append(results.UnitResources, resource.UnitResources{
			Tag:       tag,
			Resources: res,
		})
	}
	return results, nil
}

// ListModelResources returns the extended, model-related info for each
// non-pending resource of the identified service.
func (p Persistence) ListModelResources(serviceID string) ([]resource.ModelResource, error) {
	docs, err := p.resources(serviceID)
	if err != nil {
		return nil, errors.Trace(err)
	}

	var resources []resource.ModelResource
	for _, doc := range docs {
		if doc.UnitID != "" {
			continue
		}
		if doc.PendingID != "" {
			continue
		}

		res, err := doc2resource(doc)
		if err != nil {
			return nil, errors.Trace(err)
		}
		resources = append(resources, res)
	}
	return resources, nil
}

// ListPendingResources returns the extended, model-related info for
// each pending resource of the identifies service.
func (p Persistence) ListPendingResources(serviceID string) ([]resource.ModelResource, error) {
	docs, err := p.resources(serviceID)
	if err != nil {
		return nil, errors.Trace(err)
	}

	var resources []resource.ModelResource
	for _, doc := range docs {
		if doc.PendingID == "" {
			continue
		}
		// doc.UnitID will always be empty here.

		res, err := doc2resource(doc)
		if err != nil {
			return nil, errors.Trace(err)
		}
		resources = append(resources, res)
	}
	return resources, nil
}

// StageResource adds the resource in a separate staging area
// if the resource isn't already staged. If it is then
// errors.AlreadyExists is returned.
func (p Persistence) StageResource(args resource.ModelResource) error {
	// TODO(ericsnow) Ensure that the service is still there?

	if err := args.Resource.Validate(); err != nil {
		return errors.Annotate(err, "bad resource")
	}

	buildTxn := func(attempt int) ([]txn.Op, error) {
		var ops []txn.Op
		switch attempt {
		case 0:
			ops = newStagedResourceOps(args)
		case 1:
			ops = newEnsureStagedSameOps(args)
		default:
			return nil, errors.NewAlreadyExists(nil, "already staged")
		}

		return ops, nil
	}
	if err := p.base.Run(buildTxn); err != nil {
		return errors.Trace(err)
	}
	return nil
}

// UnstageResource ensures that the resource is removed
// from the staging area. If it isn't in the staging area
// then this is a noop.
func (p Persistence) UnstageResource(id string) error {
	// TODO(ericsnow) Ensure that the service is still there?

	buildTxn := func(attempt int) ([]txn.Op, error) {
		if attempt > 0 {
			// The op has no assert so we should not get here.
			return nil, errors.New("unstaging the resource failed")
		}

		ops := newRemoveStagedOps(id)
		return ops, nil
	}
	if err := p.base.Run(buildTxn); err != nil {
		return errors.Trace(err)
	}
	return nil
}

// SetUnitResource stores the resource info for a particular unit. This is an
// "upsert".
func (p Persistence) SetUnitResource(unitID string, args resource.ModelResource) error {
	// TODO(ericsnow) Ensure that the service is still there?
	if err := args.Resource.Validate(); err != nil {
		return errors.Annotate(err, "bad resource")
	}

	buildTxn := func(attempt int) ([]txn.Op, error) {
		// This is an "upsert".
		var ops []txn.Op
		switch attempt {
		case 0:
			ops = newInsertUnitResourceOps(unitID, args)
		case 1:
			ops = newUpdateUnitResourceOps(unitID, args)
		default:
			// Either insert or update will work so we should not get here.
			return nil, errors.New("setting the resource failed")
		}
		return ops, nil
	}
	if err := p.base.Run(buildTxn); err != nil {
		return errors.Trace(err)
	}
	return nil
}

// SetResource stores the resource info. This is an "upsert". If the
// resource is already staged then it is unstaged. The caller is
// responsible for getting the staging right.
func (p Persistence) SetResource(args resource.ModelResource) error {
	// TODO(ericsnow) Ensure that the service is still there?

	if err := args.Resource.Validate(); err != nil {
		return errors.Annotate(err, "bad resource")
	}

	buildTxn := func(attempt int) ([]txn.Op, error) {
		// This is an "upsert".
		var ops []txn.Op
		switch attempt {
		case 0:
			ops = newInsertResourceOps(args)
		case 1:
			ops = newUpdateResourceOps(args)
		default:
			// Either insert or update will work so we should not get here.
			return nil, errors.New("setting the resource failed")
		}
		// No matter what, we always remove any staging.
		ops = append(ops, newRemoveStagedOps(args.ID)...)
		return ops, nil
	}
	if err := p.base.Run(buildTxn); err != nil {
		return errors.Trace(err)
	}
	return nil
}
