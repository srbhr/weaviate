//                           _       _
// __      _____  __ ___   ___  __ _| |_ ___
// \ \ /\ / / _ \/ _` \ \ / / |/ _` | __/ _ \
//  \ V  V /  __/ (_| |\ V /| | (_| | ||  __/
//   \_/\_/ \___|\__,_| \_/ |_|\__,_|\__\___|
//
//  Copyright © 2016 - 2023 Weaviate B.V. All rights reserved.
//
//  CONTACT: hello@weaviate.io
//

package vectorizer

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/weaviate/weaviate/entities/models"
	"github.com/weaviate/weaviate/entities/moduletools"
	"github.com/weaviate/weaviate/entities/schema"
)

const (
	DefaultOpenAIDocumentType    = "text"
	DefaultOpenAIModel           = "ada"
	DefaultVectorizeClassName    = true
	DefaultPropertyIndexed       = true
	DefaultVectorizePropertyName = false
)

var availableOpenAITypes = []string{"text", "code"}

var availableOpenAIModels = []string{
	"ada",     // supports 001 and 002
	"babbage", // only suppports 001
	"curie",   // only suppports 001
	"davinci", // only suppports 001
}

type classSettings struct {
	cfg moduletools.ClassConfig
}

func NewClassSettings(cfg moduletools.ClassConfig) *classSettings {
	return &classSettings{cfg: cfg}
}

func (cs *classSettings) PropertyIndexed(propName string) bool {
	if cs.cfg == nil {
		// we would receive a nil-config on cross-class requests, such as Explore{}
		return DefaultPropertyIndexed
	}

	vcn, ok := cs.cfg.Property(propName)["skip"]
	if !ok {
		return DefaultPropertyIndexed
	}

	asBool, ok := vcn.(bool)
	if !ok {
		return DefaultPropertyIndexed
	}

	return !asBool
}

func (cs *classSettings) VectorizePropertyName(propName string) bool {
	if cs.cfg == nil {
		// we would receive a nil-config on cross-class requests, such as Explore{}
		return DefaultVectorizePropertyName
	}
	vcn, ok := cs.cfg.Property(propName)["vectorizePropertyName"]
	if !ok {
		return DefaultVectorizePropertyName
	}

	asBool, ok := vcn.(bool)
	if !ok {
		return DefaultVectorizePropertyName
	}

	return asBool
}

func (cs *classSettings) Model() string {
	return cs.getProperty("model", DefaultOpenAIModel)
}

func (cs *classSettings) Type() string {
	return cs.getProperty("type", DefaultOpenAIDocumentType)
}

func (cs *classSettings) ModelVersion() string {
	defaultVersion := PickDefaultModelVersion(cs.Model(), cs.Type())
	return cs.getProperty("modelVersion", defaultVersion)
}

func (cs *classSettings) ResourceName() string {
	return cs.getProperty("resourceName", "")
}

func (cs *classSettings) DeploymentID() string {
	return cs.getProperty("deploymentId", "")
}

func (cs *classSettings) IsAzure() bool {
	return cs.ResourceName() != "" && cs.DeploymentID() != ""
}

func (cs *classSettings) VectorizeClassName() bool {
	if cs.cfg == nil {
		// we would receive a nil-config on cross-class requests, such as Explore{}
		return DefaultVectorizeClassName
	}

	vcn, ok := cs.cfg.Class()["vectorizeClassName"]
	if !ok {
		return DefaultVectorizeClassName
	}

	asBool, ok := vcn.(bool)
	if !ok {
		return DefaultVectorizeClassName
	}

	return asBool
}

func (cs *classSettings) Validate(class *models.Class) error {
	if cs.cfg == nil {
		// we would receive a nil-config on cross-class requests, such as Explore{}
		return errors.New("empty config")
	}

	docType := cs.Type()
	if !cs.validateOpenAISetting(docType, availableOpenAITypes) {
		return errors.Errorf("wrong OpenAI type name, available model names are: %v", availableOpenAITypes)
	}

	model := cs.Model()
	if !cs.validateOpenAISetting(model, availableOpenAIModels) {
		return errors.Errorf("wrong OpenAI model name, available model names are: %v", availableOpenAIModels)
	}

	version := cs.ModelVersion()
	if err := cs.validateModelVersion(version, model, docType); err != nil {
		return err
	}

	err := cs.validateAzureConfig(cs.ResourceName(), cs.DeploymentID())
	if err != nil {
		return err
	}

	err = cs.validateIndexState(class, cs)
	if err != nil {
		return err
	}

	return nil
}

func (cs *classSettings) validateModelVersion(version, model, docType string) error {
	if version == "001" {
		// no restrictions
		return nil
	}

	if version == "002" {
		// only ada/davinci 002
		if model != "ada" && model != "davinci" {
			return fmt.Errorf("unsupported version %s", version)
		}
	}

	if version == "003" && model != "davinci" {
		// only davinci 003
		return fmt.Errorf("unsupported version %s", version)
	}

	if version != "002" && version != "003" {
		// all other fallback
		return fmt.Errorf("model %s is only available in version 001", model)
	}

	if docType != "text" {
		return fmt.Errorf("ada-002 no longer distinguishes between text/code, use 'text' for all use cases")
	}

	return nil
}

func (cs *classSettings) validateOpenAISetting(value string, availableValues []string) bool {
	for i := range availableValues {
		if value == availableValues[i] {
			return true
		}
	}
	return false
}

func (cs *classSettings) getProperty(name, defaultValue string) string {
	if cs.cfg == nil {
		// we would receive a nil-config on cross-class requests, such as Explore{}
		return defaultValue
	}

	model, ok := cs.cfg.Class()[name]
	if ok {
		asString, ok := model.(string)
		if ok {
			return strings.ToLower(asString)
		}
	}

	return defaultValue
}

func (cs *classSettings) validateIndexState(class *models.Class, settings ClassSettings) error {
	if settings.VectorizeClassName() {
		// if the user chooses to vectorize the classname, vector-building will
		// always be possible, no need to investigate further

		return nil
	}

	// search if there is at least one indexed, string/text prop. If found pass
	// validation
	for _, prop := range class.Properties {
		if len(prop.DataType) < 1 {
			return errors.Errorf("property %s must have at least one datatype: "+
				"got %v", prop.Name, prop.DataType)
		}

		if prop.DataType[0] != string(schema.DataTypeText) {
			// we can only vectorize text-like props
			continue
		}

		if settings.PropertyIndexed(prop.Name) {
			// found at least one, this is a valid schema
			return nil
		}
	}

	return fmt.Errorf("invalid properties: didn't find a single property which is " +
		"of type string or text and is not excluded from indexing. In addition the " +
		"class name is excluded from vectorization as well, meaning that it cannot be " +
		"used to determine the vector position. To fix this, set 'vectorizeClassName' " +
		"to true if the class name is contextionary-valid. Alternatively add at least " +
		"contextionary-valid text/string property which is not excluded from " +
		"indexing.")
}

func (cs *classSettings) validateAzureConfig(resourceName string, deploymentId string) error {
	if (resourceName == "" && deploymentId != "") || (resourceName != "" && deploymentId == "") {
		return fmt.Errorf("both resourceName and deploymentId must be provided")
	}
	return nil
}

func PickDefaultModelVersion(model, docType string) string {
	if model == "ada" && docType == "text" {
		return "002"
	}

	// for all other combinations stick with "001"
	return "001"
}
