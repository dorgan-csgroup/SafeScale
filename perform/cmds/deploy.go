/*
 * Copyright 2018, CS Systemes d'Information, http://www.c-s.fr
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

package cmds

import (
	"fmt"
	"os"

	"github.com/CS-SI/SafeScale/deploy/cluster"
	"github.com/CS-SI/SafeScale/deploy/cmds/ErrorCode"

	"github.com/CS-SI/SafeScale/utils/cli"
)

// PackageCommand ...
var PackageCommand = &cli.Command{
	Keyword: "package",
	Aliases: []string{"pkg"},

	Commands: []*cli.Command{
		deployPackageCheckCommand,
	},

	Before: func(c *cli.Command) {
		var err error
		clusterInstance, err = cluster.Get(clusterName)
		if err != nil {
			fmt.Printf("failed to get cluster '%s' information: %s\n", clusterName, err.Error())
			os.Exit(int(ErrorCode.RPC))
		}
		if clusterInstance == nil {
			fmt.Printf("cluster '%s' not found\n", clusterName)
			os.Exit(int(ErrorCode.NotFound))
		}

		pkgManagerKind = c.StringOption("kind k", "<kind>", "")
		if pkgManagerKind == "" {
			fmt.Println("Invalid empty option -k,--kind")
			os.Exit(int(ErrorCode.InvalidOption))
		}
	},

	Help: &cli.HelpContent{},
}

// deployPackageCheckCmd
var deployPackageCheckCommand = &cli.Command{
	Keyword: "check",

	Process: func(c *cli.Command) {
		pkgname := c.StringArgument("<pkgname>", "")

		if pkgname == "" {
			fmt.Println("Invalid empty argument PKGNAME")
			os.Exit(int(ErrorCode.InvalidArgument))
		}
		fmt.Println("deployPackageCheckCommand not yet implemented")
		os.Exit(int(ErrorCode.NotImplemented))
	},

	Help: &cli.HelpContent{},
}

// ServiceCommand handles "perform <clustername> service <svcname>"
var ServiceCommand = &cli.Command{
	Keyword: "service",
	Aliases: []string{"svc"},

	Commands: []*cli.Command{
		deployServiceCheckCommand,
	},

	Before: func(c *cli.Command) {
		serviceName = c.StringArgument("<svcname>", "")
		if serviceName == "" {
			fmt.Println("Invalid argument <svcname>")
			os.Exit(int(ErrorCode.NotImplemented))
		}
	},

	Help: &cli.HelpContent{},
}

// deployServiceCheckCommand ...
var deployServiceCheckCommand = &cli.Command{
	Keyword: "check",

	Process: func(c *cli.Command) {
		fmt.Println("deployServiceCheckCommand not yet implemented")
		os.Exit(int(ErrorCode.NotImplemented))
	},

	Help: &cli.HelpContent{},
}
