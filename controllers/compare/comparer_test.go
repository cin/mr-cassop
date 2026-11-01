package compare

import (
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts", Labels: map[string]string{"app": "cassandra"}},
		Spec: appsv1.StatefulSetSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{{Name: "cassandra", Image: "cassandra:1"}},
				},
			},
		},
	}
}

func TestEqualStatefulSetPodTemplate(t *testing.T) {
	tests := []struct {
		name     string
		change   func(sts *appsv1.StatefulSet)
		expected bool
	}{
		{name: "no change", change: func(sts *appsv1.StatefulSet) {}, expected: true},
		{
			name:     "statefulset labels changed",
			change:   func(sts *appsv1.StatefulSet) { sts.Labels["extra"] = "label" },
			expected: true,
		},
		{
			name:     "replicas changed",
			change:   func(sts *appsv1.StatefulSet) { sts.Spec.Replicas = new(int32) },
			expected: true,
		},
		{
			name:     "defaulted scheduler name is ignored",
			change:   func(sts *appsv1.StatefulSet) { sts.Spec.Template.Spec.SchedulerName = "default-scheduler" },
			expected: true,
		},
		{
			name:     "container image changed",
			change:   func(sts *appsv1.StatefulSet) { sts.Spec.Template.Spec.Containers[0].Image = "cassandra:2" },
			expected: false,
		},
		{
			name: "env var added",
			change: func(sts *appsv1.StatefulSet) {
				sts.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{{Name: "A", Value: "b"}}
			},
			expected: false,
		},
		{
			name:     "pod template annotation added",
			change:   func(sts *appsv1.StatefulSet) { sts.Spec.Template.Annotations = map[string]string{"a": "b"} },
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asserts := NewWithT(t)
			actual, desired := testStatefulSet(), testStatefulSet()
			tc.change(desired)

			asserts.Expect(EqualStatefulSetPodTemplate(actual, desired)).To(Equal(tc.expected))
		})
	}
}
