// Copyright 2020 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmhub

import (
	"context"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/names/v4"

	apiservererrors "github.com/juju/juju/apiserver/errors"
	"github.com/juju/juju/apiserver/facade"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/charmhub"
	"github.com/juju/juju/charmhub/transport"
	"github.com/juju/juju/environs/config"
)

var logger = loggo.GetLogger("juju.apiserver.charmhub")

// Backend defines the state methods this facade needs, so they can be
// mocked for testing.
type Backend interface {
	ModelConfig() (*config.Config, error)
}

// ClientFactory defines a factory for creating clients from a given url.
type ClientFactory interface {
	Client(string) (Client, error)
}

// Client represents a charmhub Client for making queries to the charmhub API.
type Client interface {
	URL() string
	Info(ctx context.Context, name string) (transport.InfoResponse, error)
	Find(ctx context.Context, query string) ([]transport.FindResponse, error)
}

// CharmHubAPI API provides the charmhub API facade for version 1.
type CharmHubAPI struct {
	backend Backend
	auth    facade.Authorizer
	client  Client
}

// NewFacade creates a new CharmHubAPI facade.
func NewFacade(ctx facade.Context) (*CharmHubAPI, error) {
	m, err := ctx.State().Model()
	if err != nil {
		return nil, errors.Trace(err)
	}

	return newCharmHubAPI(m, ctx.Auth(), charmhubClientFactory{})
}

func newCharmHubAPI(backend Backend, authorizer facade.Authorizer, clientFactory ClientFactory) (*CharmHubAPI, error) {
	if !authorizer.AuthClient() {
		return nil, apiservererrors.ErrPerm
	}

	modelCfg, err := backend.ModelConfig()
	if err != nil {
		return nil, errors.Trace(err)
	}
	url, _ := modelCfg.CharmhubURL()
	client, err := clientFactory.Client(url)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return &CharmHubAPI{
		auth:   authorizer,
		client: client,
	}, nil
}

// Info queries the charmhub API with a given entity ID.
func (api *CharmHubAPI) Info(arg params.Entity) (params.CharmHubEntityInfoResult, error) {
	logger.Tracef("Info(%v)", arg.Tag)

	tag, err := names.ParseApplicationTag(arg.Tag)
	if err != nil {
		return params.CharmHubEntityInfoResult{}, errors.BadRequestf("arg value is empty")
	}

	// TODO (stickupkid): Create a proper context to be used here.
	info, err := api.client.Info(context.TODO(), tag.Id())
	if err != nil {
		return params.CharmHubEntityInfoResult{}, errors.Trace(err)
	}
	return params.CharmHubEntityInfoResult{Result: convertCharmInfoResult(info, api.client.URL())}, nil
}

// Find queries the charmhub API with a given entity ID.
func (api *CharmHubAPI) Find(arg params.Query) (params.CharmHubEntityFindResult, error) {
	logger.Tracef("Find(%v)", arg.Query)

	// TODO (stickupkid): Create a proper context to be used here.
	results, err := api.client.Find(context.TODO(), arg.Query)
	if err != nil {
		return params.CharmHubEntityFindResult{}, errors.Trace(err)
	}
	return params.CharmHubEntityFindResult{Results: convertCharmFindResults(results, api.client.URL())}, nil
}

type charmhubClientFactory struct{}

func (charmhubClientFactory) Client(url string) (Client, error) {
	client, err := charmhub.NewClient(charmhub.CharmhubConfigFromURL(url))
	if err != nil {
		return nil, errors.Trace(err)
	}

	return client, nil
}
