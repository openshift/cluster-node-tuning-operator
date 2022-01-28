// Code generated by client-gen. DO NOT EDIT.

package v1

import (
	"net/http"

	v1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned/scheme"
	rest "k8s.io/client-go/rest"
)

type TunedV1Interface interface {
	RESTClient() rest.Interface
	ProfilesGetter
	TunedsGetter
}

// TunedV1Client is used to interact with features provided by the tuned.openshift.io group.
type TunedV1Client struct {
	restClient rest.Interface
}

func (c *TunedV1Client) Profiles(namespace string) ProfileInterface {
	return newProfiles(c, namespace)
}

func (c *TunedV1Client) Tuneds(namespace string) TunedInterface {
	return newTuneds(c, namespace)
}

// NewForConfig creates a new TunedV1Client for the given config.
// NewForConfig is equivalent to NewForConfigAndClient(c, httpClient),
// where httpClient was generated with rest.HTTPClientFor(c).
func NewForConfig(c *rest.Config) (*TunedV1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	httpClient, err := rest.HTTPClientFor(&config)
	if err != nil {
		return nil, err
	}
	return NewForConfigAndClient(&config, httpClient)
}

// NewForConfigAndClient creates a new TunedV1Client for the given config and http client.
// Note the http client provided takes precedence over the configured transport values.
func NewForConfigAndClient(c *rest.Config, h *http.Client) (*TunedV1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientForConfigAndClient(&config, h)
	if err != nil {
		return nil, err
	}
	return &TunedV1Client{client}, nil
}

// NewForConfigOrDie creates a new TunedV1Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *TunedV1Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new TunedV1Client for the given RESTClient.
func New(c rest.Interface) *TunedV1Client {
	return &TunedV1Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	gv := v1.SchemeGroupVersion
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *TunedV1Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
