/*
Copyright The Kubernetes Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	clientset "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned"
	lighthousev1 "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/typed/lighthouse.submariner.io/v1"
	fakelighthousev1 "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/typed/lighthouse.submariner.io/v1/fake"
	lighthousev2alpha1 "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/typed/lighthouse.submariner.io/v2alpha1"
	fakelighthousev2alpha1 "github.com/submariner-io/lighthouse/pkg/client/clientset/versioned/typed/lighthouse.submariner.io/v2alpha1/fake"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/testing"
)

// NewSimpleClientset returns a clientset that will respond with the provided objects.
// It's backed by a very simple object tracker that processes creates, updates and deletions as-is,
// without applying any validations and/or defaults. It shouldn't be considered a replacement
// for a real clientset and is mostly useful in simple unit tests.
func NewSimpleClientset(objects ...runtime.Object) *Clientset {
	o := testing.NewObjectTracker(scheme, codecs.UniversalDecoder())
	for _, obj := range objects {
		if err := o.Add(obj); err != nil {
			panic(err)
		}
	}

	cs := &Clientset{tracker: o}
	cs.discovery = &fakediscovery.FakeDiscovery{Fake: &cs.Fake}
	cs.AddReactor("*", "*", testing.ObjectReaction(o))
	cs.AddWatchReactor("*", func(action testing.Action) (handled bool, ret watch.Interface, err error) {
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := o.Watch(gvr, ns)
		if err != nil {
			return false, nil, err
		}
		return true, watch, nil
	})

	return cs
}

// Clientset implements clientset.Interface. Meant to be embedded into a
// struct to get a default implementation. This makes faking out just the method
// you want to test easier.
type Clientset struct {
	testing.Fake
	discovery *fakediscovery.FakeDiscovery
	tracker   testing.ObjectTracker
}

func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	return c.discovery
}

func (c *Clientset) Tracker() testing.ObjectTracker {
	return c.tracker
}

var _ clientset.Interface = &Clientset{}

// LighthouseV1 retrieves the LighthouseV1Client
func (c *Clientset) LighthouseV1() lighthousev1.LighthouseV1Interface {
	return &fakelighthousev1.FakeLighthouseV1{Fake: &c.Fake}
}

// LighthouseV2alpha1 retrieves the LighthouseV2alpha1Client
func (c *Clientset) LighthouseV2alpha1() lighthousev2alpha1.LighthouseV2alpha1Interface {
	return &fakelighthousev2alpha1.FakeLighthouseV2alpha1{Fake: &c.Fake}
}
