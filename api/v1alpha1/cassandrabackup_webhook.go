/*


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

package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cin/mr-cassop/controllers/util"

	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (cb *CassandraBackup) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, cb).
		WithCustomValidator(cb).
		Complete()
}

var _ webhook.CustomValidator = &CassandraBackup{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (cb *CassandraBackup) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	backup, ok := obj.(*CassandraBackup)
	if !ok {
		return nil, fmt.Errorf("object is not of type CassandraBackup")
	}
	webhookLogger.Debugf("Validating webhook has been called on create request for backup: %s", backup.Name)

	return nil, kerrors.NewAggregate(validateBackupCreateUpdate(backup))
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (cb *CassandraBackup) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	backup, ok := newObj.(*CassandraBackup)
	if !ok {
		return nil, fmt.Errorf("new object is not of type CassandraBackup")
	}
	webhookLogger.Debugf("Validating webhook has been called on update request for backup: %s", backup.Name)

	if _, ok := oldObj.(*CassandraBackup); !ok {
		return nil, fmt.Errorf("old object is not of type CassandraBackup")
	}

	return nil, kerrors.NewAggregate(validateBackupCreateUpdate(backup))
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (cb *CassandraBackup) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	webhookLogger.Debugf("Validating webhook has been called on delete request for backup: %s", cb.Name)
	return nil, nil
}

func validateBackupCreateUpdate(cb *CassandraBackup) (verrors []error) {
	if err := validateStorageLocation(cb.Spec.StorageLocation); err != nil {
		verrors = append(verrors, err)
	}

	if err := validateDuration(cb.Spec.Duration); err != nil {
		verrors = append(verrors, err)
	}

	return verrors
}

func validateDuration(durationStr string) error {
	if len(durationStr) == 0 {
		return nil
	}

	allowedDurationUnits := []string{"days", "hours", "microseconds", "milliseconds", "minutes", "nanoseconds", "seconds"}
	duration := strings.Split(durationStr, " ")
	validationErr := fmt.Errorf(
		"duration should be in format \"amount unit\", where amount is an integer value and unit is one of the following values: %v",
		allowedDurationUnits,
	)
	if len(duration) != 2 {
		return validationErr
	}

	if _, err := strconv.ParseInt(duration[0], 10, 64); err != nil {
		return validationErr
	}

	if !util.Contains(allowedDurationUnits, strings.TrimSpace(strings.ToLower(duration[1]))) {
		return validationErr
	}

	return nil
}

func validateStorageLocation(location string) error {
	index := 0
	if index = strings.Index(location, "://"); index < 0 {
		return errors.New("storage location should be in format 'protocol://backup/location'")
	}

	supportedProtocols := []string{
		string(StorageProviderS3),
		string(StorageProviderMinio),
		string(StorageProviderOracle),
		string(StorageProviderCeph),
		string(StorageProviderGCP),
		string(StorageProviderAzure),
	}
	requestedProtocol := location[:index]
	if !util.Contains(supportedProtocols, requestedProtocol) {
		return fmt.Errorf("protocol %s is not supported. Should be one of the following: %v", requestedProtocol, supportedProtocols)
	}

	return nil
}
