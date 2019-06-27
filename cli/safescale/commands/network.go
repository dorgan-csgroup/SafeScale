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

package commands

import (
	"github.com/urfave/cli"

	pb "github.com/CS-SI/SafeScale/lib"
	"github.com/CS-SI/SafeScale/lib/client"
	"github.com/CS-SI/SafeScale/lib/utils"
	clitools "github.com/CS-SI/SafeScale/lib/utils/cli"
)

// NetworkCmd command
var NetworkCmd = cli.Command{
	Name:  "network",
	Usage: "network COMMAND",
	Subcommands: []cli.Command{
		networkCreate,
		networkDelete,
		networkInspect,
		networkList,
	},
}

var networkList = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "List existing Networks (created by SafeScale)",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "all",
			Usage: "List all Networks on tenant (not only those created by SafeScale)",
		}},
	Action: func(c *cli.Context) error {
		networks, err := client.New().Network.List(c.Bool("all"), client.DefaultExecutionTimeout)
		if err != nil {
			return clitools.FailureResponse(clitools.ExitOnRPC(utils.Capitalize(client.DecorateError(err, "list of networks", false).Error())))
		}
		return clitools.SuccessResponse(networks.GetNetworks())
	},
}

var networkDelete = cli.Command{
	Name:      "delete",
	Aliases:   []string{"rm", "remove"},
	Usage:     "delete Network",
	ArgsUsage: "<Network_name> [<Network_name>...]",
	Action: func(c *cli.Context) error {
		if c.NArg() < 1 {
			_ = cli.ShowSubcommandHelp(c)
			return clitools.FailureResponse(clitools.ExitOnInvalidArgument("Missing mandatory argument <Network_name>."))
		}

		var networkList []string
		networkList = append(networkList, c.Args().First())
		networkList = append(networkList, c.Args().Tail()...)

		err := client.New().Network.Delete(networkList, client.DefaultExecutionTimeout)
		if err != nil {
			return clitools.FailureResponse(clitools.ExitOnRPC(utils.Capitalize(client.DecorateError(err, "deletion of network", false).Error())))
		}
		return clitools.SuccessResponse(nil)
	},
}

var networkInspect = cli.Command{
	Name:      "inspect",
	Aliases:   []string{"show"},
	Usage:     "inspect NETWORK",
	ArgsUsage: "<network_name>",
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			_ = cli.ShowSubcommandHelp(c)
			return clitools.FailureResponse(clitools.ExitOnInvalidArgument("Missing mandatory argument <network_name>."))
		}

		network, err := client.New().Network.Inspect(c.Args().First(), client.DefaultExecutionTimeout)
		if err != nil {
			return clitools.FailureResponse(clitools.ExitOnRPC(utils.Capitalize(client.DecorateError(err, "inspection of network", false).Error())))
		}
		return clitools.SuccessResponse(network)
	},
}

var networkCreate = cli.Command{
	Name:      "create",
	Aliases:   []string{"new"},
	Usage:     "create a network",
	ArgsUsage: "<network_name>",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "cidr",
			Value: "192.168.0.0/24",
			Usage: "cidr of the network",
		},
		cli.IntFlag{
			Name:  "cpu",
			Value: 1,
			Usage: "Number of CPU for the gateway",
		},
		cli.Float64Flag{
			Name:  "ram",
			Value: 1,
			Usage: "RAM for the gateway",
		},
		cli.IntFlag{
			Name:  "disk",
			Value: 16,
			Usage: "Disk space for the gateway",
		},
		cli.StringFlag{
			Name:  "os",
			Value: "Ubuntu 18.04",
			Usage: "Image name for the gateway",
		},
		cli.StringFlag{
			Name:  "gwname",
			Value: "",
			Usage: "Name for the gateway. Default to 'gw-<network_name>'",
		},
		cli.StringFlag{
			Name: "S, sizing",
			Usage: `Describe sizing in format "<component><operator><value>[,...]" where:
	<component> can be cpu, cpufreq, ram, disk
	<operator> can be =,<=,>= (except for disk where valid operators are only = or >=)
	<value> can be an integer (for cpu and disk) or a float (for ram) or an including interval "[<lower value>-<upper value>]"
	examples:
		--sizing "cpu<=4,ram <= 10.0,disk = 100"
		-S "cpu = [4-8], ram = [14-32]"`,
		},
	},
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			_ = cli.ShowSubcommandHelp(c)
			return clitools.FailureResponse(clitools.ExitOnInvalidArgument("Missing mandatory argument <network_name>."))
		}

		def, err := constructPBHostDefinitionFromCLI(c)
		if err != nil {
			return err
		}
		netdef := pb.NetworkDefinition{
			Cidr: c.String("cidr"),
			Name: c.Args().Get(0),
			Gateway: &pb.GatewayDefinition{
				// Cpu:  int32(c.Int("cpu")),
				// Disk: int32(c.Int("disk")),
				// Ram:  float32(c.Float64("ram")),
				// CpuFreq: ??,
				ImageId: c.String("os"),
				Name:    c.String("gwname"),
				Sizing:  def.Sizing,
			},
		}
		network, err := client.New().Network.Create(netdef, client.DefaultExecutionTimeout)
		if err != nil {
			return clitools.FailureResponse(clitools.ExitOnRPC(utils.Capitalize(client.DecorateError(err, "creation of network", true).Error())))
		}
		return clitools.SuccessResponse(network)
	},
}