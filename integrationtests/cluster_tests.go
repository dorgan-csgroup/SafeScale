/*
 * Copyright 2018-2021, CS Systemes d'Information, http://csgroup.eu
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

package integrationtests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CS-SI/SafeScale/integrationtests/enums/providers"
)

func ClusterK8S(t *testing.T, provider providers.Enum) {
	Setup(t, provider)

	names := GetNames("ClusterK8S", 0, 0, 0, 0, 0, 1)
	names.TearDown()
	defer names.TearDown()

	out, err := GetOutput("safescale -v -d cluster create + --cidr 192.168.200.0/24 --disable remotedesktop " + names.Clusters[0])
	require.Nil(t, err)
	_ = out

	command := "sudo -u cladm -i kubectl run hello-world-za --image=gcr.io/google-samples/node-hello:1.0  --port=8080"
	out, err = GetOutput("safescale ssh run " + names.Clusters[0] + "-master-1 -c \"" + command + "\"")
	require.Nil(t, err)
	_ = out

	command = "sudo -u cladm -i bash -c \\\"while kubectl get pods|grep hello-world-za|grep ContainerCreating; do kubectl get pods|grep hello-world-za|grep Running; done\\\""
	out, err = GetOutput("safescale ssh run " + names.Clusters[0] + "-master-1 -c \"" + command + "\"")
	require.Nil(t, err)
	_ = out

	out, err = GetOutput("safescale cluster inspect " + names.Clusters[0])
	require.Nil(t, err)
	_ = out

	out, err = GetOutput("safescale cluster delete --yes " + names.Clusters[0])
	require.Nil(t, err)
	fmt.Println(out)
}

func ClusterSwarm(t *testing.T, provider providers.Enum) {
	Setup(t, provider)

	names := GetNames("ClusterSwarm", 0, 0, 0, 0, 0, 1)
	names.TearDown()
	defer names.TearDown()

	out, err := GetOutput("safescale -v -d cluster create + --cidr 192.168.201.0/24 --disable remotedesktop --flavor SWARM " + names.Clusters[0])
	require.Nil(t, err)
	_ = out

	command := "docker service create --name webtest --publish 8118:80 httpd"
	out, err = GetOutput("safescale ssh run " + names.Clusters[0] + "-master-1 -c \"" + command + "\"")
	require.Nil(t, err)
	_ = out

	command = "docker service ls | grep webtest | grep httpd | grep 1/1"
	out, err = GetOutput("safescale ssh run " + names.Clusters[0] + "-master-1 -c \"" + command + "\"")
	require.Nil(t, err)
	_ = out

	command = "curl 127.0.0.1:8118"
	out, err = GetOutput("safescale ssh run " + names.Clusters[0] + "-master-1 -c \"" + command + "\"")
	require.Nil(t, err)
	require.True(t, strings.Contains(out, "It works!"))

	out, err = GetOutput("safescale host check-feature gw-net-" + names.Clusters[0] + " kong")
	require.Nil(t, err)
	_ = out

	// need --param Password=
	// out, err = GetOutput("safescale -v -d cluster add-feature " + names.Clusters[0] + " remotedesktop --skip-proxy")
	// require.Nil(t, err)

	// out, err = GetOutput("safescale -v -d cluster check-feature " + names.Clusters[0] + " remotedesktop")
	// require.Nil(t, err)

	// out, err = GetOutput("safescale -v -d cluster delete-feature " + names.Clusters[0] + " remotedesktop")
	// require.Nil(t, err)

	out, err = GetOutput("safescale cluster inspect " + names.Clusters[0])
	require.Nil(t, err)
	_ = out

	out, err = GetOutput("safescale cluster delete --yes " + names.Clusters[0])
	require.Nil(t, err)
	_ = out
}
