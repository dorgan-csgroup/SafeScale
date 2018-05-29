// Code generated by "stringer -type=Enum"; DO NOT EDIT.
/*
* Copyright 2015-2018, CS Systemes d'Information, http://www.c-s.fr
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

package NodeType

import "strconv"

const _Enum_name = "BootstrapMasterPrivateAgentPublicAgent"

var _Enum_index = [...]uint8{0, 9, 15, 27, 38}

func (i Enum) String() string {
	if i < 0 || i >= Enum(len(_Enum_index)-1) {
		return "Enum(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Enum_name[_Enum_index[i]:_Enum_index[i+1]]
}
