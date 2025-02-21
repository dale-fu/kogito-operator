// Copyright 2021 Red Hat, Inc. and/or its affiliates
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package infrastructure

import (
	"fmt"
	"github.com/kiegroup/kogito-operator/core/client/kubernetes"
	"github.com/kiegroup/kogito-operator/core/client/openshift"
	"github.com/kiegroup/kogito-operator/core/framework"
	"github.com/kiegroup/kogito-operator/core/operator"
	imgv1 "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	dockerImageKind      = "DockerImage"
	annotationKeyVersion = "version"
)

var imageStreamTagAnnotations = map[string]string{
	"iconClass":   "icon-jbpm",
	"description": "Runtime image for Kogito Service",
	"tags":        "kogito,services",
}

var imageStreamAnnotations = map[string]string{
	"openshift.io/provider-display-name": "KIE Group",
	"openshift.io/display-name":          "Kogito Service image",
}

// ImageStreamHandler ...
type ImageStreamHandler interface {
	FetchImageStream(key types.NamespacedName) (*imgv1.ImageStream, error)
	MustFetchImageStream(key types.NamespacedName) (*imgv1.ImageStream, error)
	CreateImageStreamIfNotExists(key types.NamespacedName, tag string, addFromReference bool, imageName string, insecureImageRegistry bool) (*imgv1.ImageStream, error)
	ResolveImage(key types.NamespacedName, tag string) (string, error)
	RemoveSharedImageStreamOwnerShip(key types.NamespacedName, owner client.Object) error
	FetchImageStreamForOwner(owner client.Object) ([]client.Object, error)
}

type imageStreamHandler struct {
	operator.Context
}

// NewImageStreamHandler ...
func NewImageStreamHandler(context operator.Context) ImageStreamHandler {
	return &imageStreamHandler{
		context,
	}
}

// FetchImageStream gets the deployed ImageStream shared among Kogito Custom Resources
func (i *imageStreamHandler) FetchImageStream(key types.NamespacedName) (*imgv1.ImageStream, error) {
	i.Log.Debug("fetching image stream.")
	imageStream := &imgv1.ImageStream{}
	if exists, err := kubernetes.ResourceC(i.Client).FetchWithKey(key, imageStream); err != nil {
		return nil, err
	} else if !exists {
		i.Log.Debug("Image stream not found.")
		return nil, nil
	} else {
		i.Log.Debug("Successfully fetch deployed image stream")
		return imageStream, nil
	}
}

func (i *imageStreamHandler) FetchImageStreamForOwner(owner client.Object) ([]client.Object, error) {
	i.Log.Debug("fetching image stream for given owner.")
	objectTypes := []client.ObjectList{&imgv1.ImageStreamList{}}
	resources, err := kubernetes.ResourceC(i.Client).ListAll(objectTypes, owner.GetNamespace(), owner)
	if err != nil {
		return nil, err
	}
	return resources[reflect.TypeOf(imgv1.ImageStream{})], nil
}

// MustFetchImageStream gets the deployed ImageStream shared among Kogito Custom Resources. If not found then return error.
func (i *imageStreamHandler) MustFetchImageStream(key types.NamespacedName) (*imgv1.ImageStream, error) {
	if imageStream, err := i.FetchImageStream(key); err != nil {
		return nil, err
	} else if imageStream == nil {
		return nil, fmt.Errorf("image stream with name %s not found in namespace %s", key.Name, key.Namespace)
	} else {
		i.Log.Debug("Successfully fetch deployed kogito infra reference")
		return imageStream, nil
	}
}

func (i *imageStreamHandler) CreateImageStreamIfNotExists(key types.NamespacedName, tag string, addFromReference bool, imageName string, insecureImageRegistry bool) (*imgv1.ImageStream, error) {
	imageStream, err := i.FetchImageStream(key)
	if err != nil {
		return nil, err
	}
	if imageStream == nil {
		imageStream = i.createImageStream(key)
	}

	isTagExists := i.checkIfTagExists(imageStream, tag)
	if !isTagExists {
		tagReference := i.createImageStreamTag(tag, addFromReference, imageName, insecureImageRegistry)
		tagReferences := append(imageStream.Spec.Tags, tagReference)
		imageStream.Spec.Tags = tagReferences
	}
	return imageStream, nil
}

// createImageStream creates the ImageStream referencing the given namespace.
func (i *imageStreamHandler) createImageStream(key types.NamespacedName) *imgv1.ImageStream {
	i.Log.Debug("Creating new Image stream.", "imageStream name", key.Name)
	return &imgv1.ImageStream{
		ObjectMeta: v1.ObjectMeta{
			Name:        key.Name,
			Namespace:   key.Namespace,
			Annotations: imageStreamAnnotations,
		},
		Spec: imgv1.ImageStreamSpec{
			LookupPolicy: imgv1.ImageLookupPolicy{Local: true},
		},
	}
}

func (i *imageStreamHandler) checkIfTagExists(imageStream *imgv1.ImageStream, tag string) bool {
	i.Log.Debug("Checking if tag exists in image stream", "tag", tag)
	for _, existingTag := range imageStream.Spec.Tags {
		if existingTag.Name == tag {
			i.Log.Debug("tag exists in image stream", "tag", tag)
			return true
		}
	}
	i.Log.Debug("tag not exists in image stream", "tag", tag)
	return false
}

// Adds a docker image in the "From" reference based on the given image if `addFromReference` is set to `true`
func (i *imageStreamHandler) createImageStreamTag(tag string, addFromReference bool, imageName string, insecureImageRegistry bool) imgv1.TagReference {
	i.Log.Debug("Create new tag reference", "tag", tag)
	imageStreamTagAnnotations[annotationKeyVersion] = tag
	tagReference := imgv1.TagReference{
		Name:            tag,
		Annotations:     imageStreamTagAnnotations,
		ReferencePolicy: imgv1.TagReferencePolicy{Type: imgv1.LocalTagReferencePolicy},
		ImportPolicy:    imgv1.TagImportPolicy{Insecure: insecureImageRegistry},
	}
	if addFromReference {
		tagReference.From = &corev1.ObjectReference{
			Kind: dockerImageKind,
			Name: imageName,
		}
	}
	return tagReference
}

func (i *imageStreamHandler) ResolveImage(key types.NamespacedName, tag string) (string, error) {
	i.Log.Debug("Going to resolve image using ImageStream.")
	if err := i.validateTagStatus(key, tag); err != nil {
		return "", err
	}
	// the image is on an ImageStreamTag object
	ist, err := i.fetchTag(key, tag)
	if err != nil || ist == nil {
		return "", err
	}
	return ist.Image.DockerImageReference, nil
}

// ValidateTagStatus process any error occurs while processing tag in image stream(ex. reference image is invalid) then error message need to
// be fetched from the ImportSuccess status type of that tag. If ImportSuccess condition type is not available in image stream
// status them tag its mean tag is successfully processed.
func (i *imageStreamHandler) validateTagStatus(key types.NamespacedName, tag string) error {
	i.Log.Debug("validate image stream tag status for any error while processing tag.")
	is, err := i.FetchImageStream(key)
	if err != nil {
		return err
	}
	if is == nil {
		return nil
	}
	tagCondition := i.findTagStatusCondition(is, tag)
	if tagCondition == nil {
		return nil
	}
	if tagCondition.Status == corev1.ConditionFalse {
		return fmt.Errorf(tagCondition.Message)
	}
	return nil
}

// findTagStatusCondition finds the ImportSuccess conditionType in conditions.
func (i *imageStreamHandler) findTagStatusCondition(is *imgv1.ImageStream, tag string) *imgv1.TagEventCondition {
	tagEvents := is.Status.Tags
	for _, tagEvent := range tagEvents {
		if tagEvent.Tag == tag {
			for _, condition := range tagEvent.Conditions {
				if condition.Type == imgv1.ImportSuccess {
					return &condition
				}
			}
		}
	}
	return nil
}

func (i *imageStreamHandler) fetchTag(key types.NamespacedName, tag string) (*imgv1.ImageStreamTag, error) {
	i.Log.Debug("fetching image stream tag", "tag", tag)
	ist, err := openshift.ImageStreamC(i.Client).FetchTag(key, tag)
	if err != nil {
		i.Log.Error(err, "Error occurs while fetching image stream tag", "tag", tag)
		return nil, err
	} else if ist == nil {
		i.Log.Debug("Image stream tag not found.", "tag", tag)
		return nil, nil
	}
	i.Log.Debug("Successfully fetch deployed image stream tag")
	return ist, nil
}

func (i *imageStreamHandler) RemoveSharedImageStreamOwnerShip(key types.NamespacedName, owner client.Object) (err error) {
	i.Log.Info("Removing imageStream ownership", "imageStream", key.Name, "owner", owner.GetName())
	is, err := i.FetchImageStream(key)
	if err != nil || is == nil {
		return
	}
	ownerRefRemoved := framework.RemoveSharedOwnerReference(owner, is)
	if ownerRefRemoved {
		if err = kubernetes.ResourceC(i.Client).Update(is); err != nil {
			return err
		}
		i.Log.Debug("Successfully removed imageStream ownership", "imageStream", is.GetName(), "owner", owner.GetName())
		return
	}
	i.Log.Debug("Owner reference doesn't match. Skip to remove owner reference.", "imageStream", is.GetName(), "owner", owner.GetName())
	return
}
