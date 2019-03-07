/*
 * Copyright 2018-2019, CS Systemes d'Information, http://www.c-s.fr
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package propertiesv1

import (
	"time"

	"github.com/CS-SI/SafeScale/safescale/server/cluster/enums/ClusterState"
	"github.com/CS-SI/SafeScale/safescale/server/cluster/enums/Property"
	"github.com/CS-SI/SafeScale/utils/serialize"
)

// State contains the bare minimum information about a cluster
type State struct {
	// State of the cluster
	State ClusterState.Enum
	// StateCollectInterval in seconds
	StateCollectInterval time.Duration `json:"state_collect_interval,omitempty"`
}

func newState() *State {
	return &State{}
}

// Content ... (serialize.Property interface)
func (s *State) Content() interface{} {
	return s
}

// Clone ... (serialize.Property interface)
func (s *State) Clone() serialize.Property {
	return newState().Replace(s)
}

// Replace ... (serialize.Property interface)
func (s *State) Replace(p serialize.Property) serialize.Property {
	*s = *p.(*State)
	return s
}

func init() {
	serialize.PropertyTypeRegistry.Register("clusters", Property.StateV1, &State{})
}