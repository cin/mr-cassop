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
	"fmt"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (cr *CassandraRestore) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, cr).
		WithCustomValidator(cr).
		Complete()
}

var _ webhook.CustomValidator = &CassandraRestore{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (cr *CassandraRestore) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	restore, ok := obj.(*CassandraRestore)
	if !ok {
		return nil, fmt.Errorf("object is not of type CassandraRestore")
	}
	webhookLogger.Debugf("Validating webhook has been called on create request for restore: %s", restore.Name)

	return nil, kerrors.NewAggregate(validateRestoreCreateUpdate(restore))
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (cr *CassandraRestore) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	restore, ok := newObj.(*CassandraRestore)
	if !ok {
		return nil, fmt.Errorf("new object is not of type CassandraRestore")
	}
	webhookLogger.Debugf("Validating webhook has been called on update request for restore: %s", restore.Name)

	if _, ok := oldObj.(*CassandraRestore); !ok {
		return nil, fmt.Errorf("old object is not of type CassandraRestore")
	}

	return nil, kerrors.NewAggregate(validateRestoreCreateUpdate(restore))
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (cr *CassandraRestore) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	webhookLogger.Debugf("Validating webhook has been called on delete request for restore: %s", cr.Name)
	return nil, nil
}

func validateRestoreCreateUpdate(cr *CassandraRestore) (verrors []error) {
	if len(cr.Spec.CassandraBackup) == 0 {
		if len(cr.Spec.StorageLocation) == 0 || len(cr.Spec.SnapshotTag) == 0 || len(cr.Spec.SecretName) == 0 {
			verrors = append(verrors, errors.New(".spec.storageLocation, .spec.snapshotTag and .spec.secretName should be set if .spec.cassandraBackup is not set"))
		} else {
			if err := validateStorageLocation(cr.Spec.StorageLocation); err != nil {
				verrors = append(verrors, err)
			}
		}
	}

	return verrors
}
