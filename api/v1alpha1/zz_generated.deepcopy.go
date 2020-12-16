// +build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoScheduling) DeepCopyInto(out *AutoScheduling) {
	*out = *in
	if in.ExcludedKeyspaces != nil {
		in, out := &in.ExcludedKeyspaces, &out.ExcludedKeyspaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoScheduling.
func (in *AutoScheduling) DeepCopy() *AutoScheduling {
	if in == nil {
		return nil
	}
	out := new(AutoScheduling)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Cassandra) DeepCopyInto(out *Cassandra) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Cassandra.
func (in *Cassandra) DeepCopy() *Cassandra {
	if in == nil {
		return nil
	}
	out := new(Cassandra)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CassandraCluster) DeepCopyInto(out *CassandraCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CassandraCluster.
func (in *CassandraCluster) DeepCopy() *CassandraCluster {
	if in == nil {
		return nil
	}
	out := new(CassandraCluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CassandraCluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CassandraClusterList) DeepCopyInto(out *CassandraClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]CassandraCluster, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CassandraClusterList.
func (in *CassandraClusterList) DeepCopy() *CassandraClusterList {
	if in == nil {
		return nil
	}
	out := new(CassandraClusterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CassandraClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CassandraClusterSpec) DeepCopyInto(out *CassandraClusterSpec) {
	*out = *in
	if in.DCs != nil {
		in, out := &in.DCs, &out.DCs
		*out = make([]DC, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Cassandra != nil {
		in, out := &in.Cassandra, &out.Cassandra
		*out = new(Cassandra)
		(*in).DeepCopyInto(*out)
	}
	in.SystemKeyspaces.DeepCopyInto(&out.SystemKeyspaces)
	in.Prober.DeepCopyInto(&out.Prober)
	in.Kwatcher.DeepCopyInto(&out.Kwatcher)
	if in.Reaper != nil {
		in, out := &in.Reaper, &out.Reaper
		*out = new(Reaper)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CassandraClusterSpec.
func (in *CassandraClusterSpec) DeepCopy() *CassandraClusterSpec {
	if in == nil {
		return nil
	}
	out := new(CassandraClusterSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CassandraClusterStatus) DeepCopyInto(out *CassandraClusterStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CassandraClusterStatus.
func (in *CassandraClusterStatus) DeepCopy() *CassandraClusterStatus {
	if in == nil {
		return nil
	}
	out := new(CassandraClusterStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DC) DeepCopyInto(out *DC) {
	*out = *in
	if in.Replicas != nil {
		in, out := &in.Replicas, &out.Replicas
		*out = new(int32)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DC.
func (in *DC) DeepCopy() *DC {
	if in == nil {
		return nil
	}
	out := new(DC)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Jolokia) DeepCopyInto(out *Jolokia) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Jolokia.
func (in *Jolokia) DeepCopy() *Jolokia {
	if in == nil {
		return nil
	}
	out := new(Jolokia)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Kwatcher) DeepCopyInto(out *Kwatcher) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Kwatcher.
func (in *Kwatcher) DeepCopy() *Kwatcher {
	if in == nil {
		return nil
	}
	out := new(Kwatcher)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Prober) DeepCopyInto(out *Prober) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
	in.Jolokia.DeepCopyInto(&out.Jolokia)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Prober.
func (in *Prober) DeepCopy() *Prober {
	if in == nil {
		return nil
	}
	out := new(Prober)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Reaper) DeepCopyInto(out *Reaper) {
	*out = *in
	if in.DCs != nil {
		in, out := &in.DCs, &out.DCs
		*out = make([]DC, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]v1.Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	in.Resources.DeepCopyInto(&out.Resources)
	in.ScheduleRepairs.DeepCopyInto(&out.ScheduleRepairs)
	in.AutoScheduling.DeepCopyInto(&out.AutoScheduling)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Reaper.
func (in *Reaper) DeepCopy() *Reaper {
	if in == nil {
		return nil
	}
	out := new(Reaper)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Repair) DeepCopyInto(out *Repair) {
	*out = *in
	if in.Tables != nil {
		in, out := &in.Tables, &out.Tables
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Datacenters != nil {
		in, out := &in.Datacenters, &out.Datacenters
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Repair.
func (in *Repair) DeepCopy() *Repair {
	if in == nil {
		return nil
	}
	out := new(Repair)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ScheduleRepairs) DeepCopyInto(out *ScheduleRepairs) {
	*out = *in
	if in.Repairs != nil {
		in, out := &in.Repairs, &out.Repairs
		*out = make([]Repair, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ScheduleRepairs.
func (in *ScheduleRepairs) DeepCopy() *ScheduleRepairs {
	if in == nil {
		return nil
	}
	out := new(ScheduleRepairs)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SystemKeyspaceDC) DeepCopyInto(out *SystemKeyspaceDC) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SystemKeyspaceDC.
func (in *SystemKeyspaceDC) DeepCopy() *SystemKeyspaceDC {
	if in == nil {
		return nil
	}
	out := new(SystemKeyspaceDC)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SystemKeyspaces) DeepCopyInto(out *SystemKeyspaces) {
	*out = *in
	if in.Names != nil {
		in, out := &in.Names, &out.Names
		*out = make([]KeyspaceName, len(*in))
		copy(*out, *in)
	}
	if in.DCs != nil {
		in, out := &in.DCs, &out.DCs
		*out = make([]SystemKeyspaceDC, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SystemKeyspaces.
func (in *SystemKeyspaces) DeepCopy() *SystemKeyspaces {
	if in == nil {
		return nil
	}
	out := new(SystemKeyspaces)
	in.DeepCopyInto(out)
	return out
}
