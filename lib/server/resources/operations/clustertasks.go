/*
 * Copyright 2018-2020, CS Systemes d'Information, http://csgroup.eu
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

package operations

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/clusternodetype"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/clusterproperty"
	propertiesv2 "github.com/CS-SI/SafeScale/lib/server/resources/properties/v2"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/serialize"
	"github.com/CS-SI/SafeScale/lib/utils/strprocess"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

func (c *cluster) taskStartHost(task concurrency.Task, params concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// FIXME: validate params
	return nil, c.service.StartHost(params.(string))
}

func (c *cluster) taskStopHost(task concurrency.Task, params concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// FIXME: validate params
	return nil, c.service.StopHost(params.(string))
}

type taskInstallGatewayParameters struct {
	Host resources.Host
}

// taskInstallGateway installs necessary components on one gateway
// This function is intended to be call as a goroutine
func (c *cluster) taskInstallGateway(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), params).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	p, ok := params.(taskInstallGatewayParameters)
	if !ok {
		return result, fail.InvalidParameterError("params", "must be a 'taskInstallGatewayParameters'")
	}
	if p.Host.IsNull() {
		return result, fail.InvalidParameterError("params.Host", "cannot be null value of 'resources.Host'")
	}

	hostLabel := p.Host.GetName()
	logrus.Debugf("[%s] starting installation...", hostLabel)

	if _, xerr = p.Host.WaitSSHReady(task, temporal.GetHostTimeout()); xerr != nil {
		return nil, xerr
	}

	// Installs docker and docker-compose on gateway
	if xerr = c.installDocker(task, p.Host, hostLabel); xerr != nil {
		return nil, xerr
	}

	// Installs proxycache server on gateway (if not disabled)
	if xerr = c.installProxyCacheServer(task, p.Host, hostLabel); xerr != nil {
		return nil, xerr
	}

	// Installs requirements as defined by cluster Flavor (if it exists)
	if xerr = c.installNodeRequirements(task, clusternodetype.Gateway, p.Host, hostLabel); xerr != nil {
		return nil, xerr
	}

	logrus.Debugf("[%s] preparation successful", hostLabel)
	return nil, nil
}

type taskConfigureGatewayParameters struct {
	Host resources.Host
}

// taskConfigureGateway prepares one gateway
// This function is intended to be call as a goroutine
func (c cluster) taskConfigureGateway(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// validate and convert parameters
	p, ok := params.(taskConfigureGatewayParameters)
	if !ok {
		return result, fail.InvalidParameterError("params", "must be a 'taskConfigureGatewayParameters'")
	}
	if p.Host.IsNull() {
		return result, fail.InvalidParameterError("params.Host", "cannot be null value of 'resources.Host'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%v)", params).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	logrus.Debugf("[%s] starting configuration...", p.Host.GetName())

	if c.makers.ConfigureGateway != nil {
		if xerr = c.makers.ConfigureGateway(task, &c); xerr != nil {
			return nil, xerr
		}
	}

	logrus.Debugf("[%s] configuration successful in [%s].", p.Host.GetName(), tracer.Stopwatch().String())
	return nil, nil
}

type taskCreateMastersParameters struct {
	Count      uint
	MastersDef abstract.HostSizingRequirements
	NoKeep     bool
}

// taskCreateMasters creates masters
// This function is intended to be call as a goroutine
func (c cluster) taskCreateMasters(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%v)", params).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	// Convert and validate parameters
	p, ok := params.(taskCreateMastersParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskCreteMastersParameters'")
	}
	if p.Count < 1 {
		return nil, fail.InvalidParameterError("params.Count", "cannot be an integer less than 1")
	}

	clusterName := c.GetName()

	if p.Count == 0 {
		logrus.Debugf("[cluster %s] no masters to create.", clusterName)
		return nil, nil
	}

	logrus.Debugf("[cluster %s] creating %d master%s...", clusterName, p.Count, strprocess.Plural(p.Count))

	var subtasks []concurrency.Task
	timeout := temporal.GetContextTimeout() + time.Duration(p.Count)*time.Minute
	var i uint
	for ; i < p.Count; i++ {
		subtask, xerr := task.StartInSubtask(c.taskCreateMaster, taskCreateMasterParameters{
			Index:     i + 1,
			MasterDef: p.MastersDef,
			Timeout:   timeout,
			NoKeep:    p.NoKeep,
		})
		if xerr != nil {
			return nil, xerr
		}
		subtasks = append(subtasks, subtask)
	}
	var errs []string
	for _, s := range subtasks {
		_, state := s.Wait()
		if state != nil {
			errs = append(errs, state.Error())
		}
	}
	if len(errs) > 0 {
		msg := strings.Join(errs, "\n")
		return nil, fail.NewError("[cluster %s] failed to create master(s): %s", clusterName, msg)
	}

	logrus.Debugf("[cluster %s] masters creation successful.", clusterName)
	return nil, nil
}

type taskCreateMasterParameters struct {
	Index     uint
	MasterDef abstract.HostSizingRequirements
	Timeout   time.Duration
	NoKeep    bool
}

// taskCreateMaster creates one master
// This function is intended to be call as a goroutine
func (c *cluster) taskCreateMaster(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%v)", params).Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())
	defer fail.OnPanic(&xerr)

	// Convert and validate parameters
	p, ok := params.(taskCreateMasterParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskCreateMasterParameters'")
	}

	if p.Index < 1 {
		return nil, fail.InvalidParameterError("params.Index", "must be an integer greater than 0")
	}

	hostLabel := fmt.Sprintf("master #%d", p.Index)
	logrus.Debugf("[%s] starting Host creation...", hostLabel)

	netCfg, xerr := c.GetNetworkConfig(task)
	if xerr != nil {
		return nil, xerr
	}
	subnet, xerr := LoadSubnet(task, c.service, "", netCfg.SubnetID)
	if xerr != nil {
		return nil, xerr
	}

	hostReq := abstract.HostRequest{}
	xerr = subnet.Inspect(task, func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
		as, ok := clonable.(*abstract.Subnet)
		if !ok {
			return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}
		hostReq.Subnets = []*abstract.Subnet{as}
		return nil
	})
	if xerr != nil {
		return nil, xerr
	}

	if hostReq.ResourceName, xerr = c.buildHostname(task, "master", clusternodetype.Master); xerr != nil {
		return nil, xerr
	}

	hostReq.DefaultRouteIP = netCfg.DefaultRouteIP
	hostReq.PublicIP = false
	// hostReq.ImageID = def.Image

	rh, xerr := NewHost(c.service)
	if xerr != nil {
		return nil, xerr
	}

	if _, xerr = rh.Create(task, hostReq, p.MasterDef); xerr != nil {
		return nil, xerr
	}

	// Updates cluster metadata to keep track of created IPAddress, before testing if an error occurred during the creation
	xerr = c.Alter(task, func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		// References new node in cluster
		return props.Alter(task, clusterproperty.NodesV2, func(clonable data.Clonable) fail.Error {
			nodesV2 := clonable.(*propertiesv2.ClusterNodes)
			nodesV2.GlobalLastIndex++
			pubIP, innerXErr := rh.GetPublicIP(task)
			if innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrNotFound:
					// no public IP, this can happen, continue
				default:
					return innerXErr
				}
			}

			privIP, innerXErr := rh.GetPrivateIP(task)
			if innerXErr != nil {
				return innerXErr
			}
			node := &propertiesv2.ClusterNode{
				ID:          rh.GetID(),
				NumericalID: nodesV2.GlobalLastIndex,
				Name:        rh.GetName(),
				PrivateIP:   privIP,
				PublicIP:    pubIP,
			}
			nodesV2.Masters = append(nodesV2.Masters, node)
			return nil
		})
	})
	if xerr != nil {
		if p.NoKeep {
			if derr := rh.Delete(task); derr != nil {
				_ = xerr.AddConsequence(derr)
			}
		}
		return nil, fail.Wrap(xerr, "[%s] Host creation failed")
	}

	hostLabel = fmt.Sprintf("%s (%s)", hostLabel, rh.GetName())
	logrus.Debugf("[%s] Host creation successful", hostLabel)

	if xerr = c.installProxyCacheClient(task, rh, hostLabel); xerr != nil {
		return nil, xerr
	}

	// Installs cluster-level system requirements...
	if xerr = c.installNodeRequirements(task, clusternodetype.Master, rh, hostLabel); xerr != nil {
		return nil, xerr
	}

	logrus.Debugf("[%s] Host creation successful.", hostLabel)
	return nil, nil
}

// taskConfigureMasters configure masters
// This function is intended to be call as a goroutine
func (c *cluster) taskConfigureMasters(task concurrency.Task, _ concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster")).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	list, xerr := c.ListMasterIDs(task)
	if xerr != nil {
		return nil, xerr
	}
	if len(list) == 0 {
		return nil, nil
	}

	logrus.Debugf("[cluster %s] Configuring masters...", c.GetName())
	started := time.Now()

	var subtasks []concurrency.Task
	masters, xerr := c.ListMasterIDs(task)
	if xerr != nil {
		return nil, xerr
	}

	var errors []error

	for i, hostID := range masters {
		host, xerr := LoadHost(task, c.GetService(), hostID)
		if xerr != nil {
			logrus.Warnf("failed to get metadata of Host: %s", xerr.Error())
			errors = append(errors, xerr)
			continue
		}
		subtask, xerr := task.StartInSubtask(c.taskConfigureMaster, taskConfigureMasterParameters{
			Index: i + 1,
			Host:  host,
		})
		if xerr != nil {
			errors = append(errors, xerr)
		}
		subtasks = append(subtasks, subtask)
	}

	for _, s := range subtasks {
		_, state := s.Wait()
		if state != nil {
			errors = append(errors, state)
		}
	}
	if len(errors) > 0 {
		return nil, fail.NewErrorList(errors)
	}

	logrus.Debugf("[cluster %s] Masters configuration successful in [%s].", c.GetName(), temporal.FormatDuration(time.Since(started)))
	return nil, nil
}

type taskConfigureMasterParameters struct {
	Index uint
	Host  resources.Host
}

// taskConfigureMaster configures one master
// This function is intended to be call as a goroutine
func (c *cluster) taskConfigureMaster(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%v)", params).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	// Convert and validate params
	p, ok := params.(taskConfigureMasterParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskConfigureMasterParameters'")
	}

	if p.Index < 1 {
		return nil, fail.InvalidParameterError("params.Index", "cannot be an integer less than 1")
	}
	if p.Host.IsNull() {
		return nil, fail.InvalidParameterError("params.Host", "cannot be null value of 'resources.Host'")
	}

	started := time.Now()

	hostLabel := fmt.Sprintf("master #%d (%s)", p.Index, p.Host.GetName())
	logrus.Debugf("[%s] starting configuration...", hostLabel)

	// install docker feature (including docker-compose)
	if xerr = c.installDocker(task, p.Host, hostLabel); xerr != nil {
		return nil, xerr
	}

	if c.makers.ConfigureNode != nil {
		if xerr = c.makers.ConfigureMaster(task, c, p.Index, p.Host); xerr != nil {
			return nil, xerr
		}
		logrus.Debugf("[%s] configuration successful in [%s].", hostLabel, temporal.FormatDuration(time.Since(started)))
		return nil, nil
	}
	// Not finding a callback isn't an error, so return nil in this case
	return nil, nil
}

type taskCreateNodesParameters struct {
	Count    uint
	Public   bool
	NodesDef abstract.HostSizingRequirements
	NoKeep   bool
}

// taskCreateNodes creates nodes
// This function is intended to be call as a goroutine
func (c *cluster) taskCreateNodes(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// Convert then validate params
	p, ok := params.(taskCreateNodesParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskCreateNodesParameters'")
	}

	if p.Count < 1 {
		return nil, fail.InvalidParameterError("params.Count", "cannot be an integer less than 1")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%d, %v)", p.Count, p.Public).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	clusterName := c.GetName()

	if p.Count == 0 {
		logrus.Debugf("[cluster %s] no nodes to create.", clusterName)
		return nil, nil
	}
	logrus.Debugf("[cluster %s] creating %d node%s...", clusterName, p.Count, strprocess.Plural(p.Count))

	timeout := temporal.GetContextTimeout() + time.Duration(p.Count)*time.Minute
	var subtasks []concurrency.Task
	for i := uint(1); i <= p.Count; i++ {
		subtask, xerr := task.StartInSubtask(c.taskCreateNode, taskCreateNodeParameters{
			Index:   i,
			NodeDef: p.NodesDef,
			Timeout: timeout,
			NoKeep:  p.NoKeep,
		})
		if xerr != nil {
			return nil, xerr
		}

		subtasks = append(subtasks, subtask)
	}

	var errs []error
	for _, s := range subtasks {
		_, state := s.Wait()
		if state != nil {
			errs = append(errs, state)
		}
	}
	if len(errs) > 0 {
		return nil, fail.NewErrorList(errs)
	}

	logrus.Debugf("[cluster %s] %d node%s creation successful.", clusterName, p.Count, strprocess.Plural(p.Count))
	return nil, nil
}

type taskCreateNodeParameters struct {
	Index   uint
	NodeDef abstract.HostSizingRequirements
	Timeout time.Duration // Not used currently
	NoKeep  bool
}

// taskCreateNode creates a node in the Cluster
// This function is intended to be call as a goroutine
func (c *cluster) taskCreateNode(task concurrency.Task, params concurrency.TaskParameters) (result concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	defer fail.OnPanic(&xerr)

	// Convert then validate parameters
	p, ok := params.(taskCreateNodeParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a data.Map")
	}

	if p.Index < 1 {
		return nil, fail.InvalidParameterError("params.Index", "cannot be an integer less than 1")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%d)", p.Index).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	hostLabel := fmt.Sprintf("node #%d", p.Index)
	logrus.Debugf("[%s] starting Host creation...", hostLabel)

	netCfg, xerr := c.GetNetworkConfig(task)
	if xerr != nil {
		return nil, xerr
	}

	subnet, xerr := LoadSubnet(task, c.service, "", netCfg.SubnetID)
	if xerr != nil {
		return nil, xerr
	}

	// Create the rh
	hostReq := abstract.HostRequest{}
	hostReq.ResourceName, xerr = c.buildHostname(task, "node", clusternodetype.Node)
	if xerr != nil {
		return nil, xerr
	}

	xerr = subnet.Inspect(task, func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
		as, ok := clonable.(*abstract.Subnet)
		if !ok {
			return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		hostReq.Subnets = []*abstract.Subnet{as}
		return nil
	})
	if xerr != nil {
		return nil, xerr
	}

	if hostReq.DefaultRouteIP, xerr = subnet.GetDefaultRouteIP(task); xerr != nil {
		return nil, xerr
	}

	hostReq.PublicIP = false
	// hostReq.ImageID = def.Image

	// if timeout < temporal.GetLongOperationTimeout() {
	// 	timeout = temporal.GetLongOperationTimeout()
	// }

	rh, xerr := NewHost(c.GetService())
	if xerr != nil {
		return nil, xerr
	}

	if _, xerr = rh.Create(task, hostReq, p.NodeDef); xerr != nil {
		return nil, xerr
	}

	defer func() {
		if xerr != nil && p.NoKeep {
			if derr := rh.Delete(task); derr != nil {
				_ = xerr.AddConsequence(derr)
			}
		}
	}()

	var node *propertiesv2.ClusterNode
	xerr = c.Alter(task, func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(task, clusterproperty.NodesV2, func(clonable data.Clonable) fail.Error {
			nodesV2, ok := clonable.(*propertiesv2.ClusterNodes)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.ClusterNodes' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}
			// Registers the new Agent in the swarmCluster struct
			nodesV2.GlobalLastIndex++
			pubIP, innerXErr := rh.GetPublicIP(task)
			if innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrNotFound:
					// No public IP, this can happen; continue
				default:
					return innerXErr
				}
			}

			privIP, innerXErr := rh.GetPrivateIP(task)
			if innerXErr != nil {
				return innerXErr
			}

			node = &propertiesv2.ClusterNode{
				ID:          rh.GetID(),
				NumericalID: nodesV2.GlobalLastIndex,
				Name:        rh.GetName(),
				PrivateIP:   privIP,
				PublicIP:    pubIP,
			}
			nodesV2.PrivateNodes = append(nodesV2.PrivateNodes, node)
			return nil
		})
	})
	if xerr != nil {
		return nil, fail.Wrap(xerr, "[%s] creation failed", hostLabel)
	}

	hostLabel = fmt.Sprintf("node #%d (%s)", p.Index, rh.GetName())

	if xerr = c.installProxyCacheClient(task, rh, hostLabel); xerr != nil {
		return nil, xerr
	}

	if xerr = c.installNodeRequirements(task, clusternodetype.Node, rh, hostLabel); xerr != nil {
		return nil, xerr
	}

	logrus.Debugf("[%s] Host creation successful.", hostLabel)
	return rh, nil
}

// taskConfigureNodes configures nodes
// This function is intended to be call as a goroutine
func (c *cluster) taskConfigureNodes(task concurrency.Task, _ concurrency.TaskParameters) (_ concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	clusterName := c.GetName()

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster")).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	list, err := c.ListNodeIDs(task)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		logrus.Debugf("[cluster %s] no nodes to configure.", clusterName)
		return nil, nil
	}

	logrus.Debugf("[cluster %s] configuring nodes...", clusterName)

	var (
		host   resources.Host
		i      uint
		hostID string
		errs   []error
	)

	svc := c.GetService()
	var subtasks []concurrency.Task
	for _, hostID = range list {
		i++
		if host, xerr = LoadHost(task, svc, hostID); xerr != nil {
			errs = append(errs, fail.Wrap(xerr, "failed to get metadata of Host '%s'", hostID))
			continue
		}
		subtask, xerr := task.StartInSubtask(c.taskConfigureNode, taskConfigureNodeParameters{
			Index: i,
			Host:  host,
		})
		if xerr != nil {
			return nil, xerr
		}

		subtasks = append(subtasks, subtask)
	}

	for _, s := range subtasks {
		if _, xerr := s.Wait(); xerr != nil {
			errs = append(errs, xerr)
		}
	}
	if len(errs) > 0 {
		return nil, fail.NewErrorList(errs)
	}

	logrus.Debugf("[cluster %s] nodes configuration successful.", clusterName)
	return nil, nil
}

type taskConfigureNodeParameters struct {
	Index uint
	Host  resources.Host
}

// taskConfigureNode configure one node
// This function is intended to be call as a goroutine
func (c *cluster) taskConfigureNode(task concurrency.Task, params concurrency.TaskParameters) (_ concurrency.TaskResult, xerr fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// Convert and validate params
	p, ok := params.(taskConfigureNodeParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskConfigureNodeParameters'")
	}
	if p.Index < 1 {
		return nil, fail.InvalidParameterError("params.Index", "cannot be an integer less than 1")
	}
	if p.Host.IsNull() {
		return nil, fail.InvalidParameterError("params.Host", "cannot be null value of 'resources.Host'")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.cluster"), "(%d, %s)", p.Index, p.Host.GetName()).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage())

	hostLabel := fmt.Sprintf("node #%d (%s)", p.Index, p.Host.GetName())
	logrus.Debugf("[%s] starting configuration...", hostLabel)

	// Docker and docker-compose installation is mandatory on all nodes
	if xerr = c.installDocker(task, p.Host, hostLabel); xerr != nil {
		return nil, xerr
	}

	// Now configures node specifically for cluster flavor
	if c.makers.ConfigureNode == nil {
		return nil, nil
	}
	if xerr = c.makers.ConfigureNode(task, c, p.Index, p.Host); xerr != nil {
		logrus.Error(xerr.Error())
		return nil, xerr
	}
	logrus.Debugf("[%s] configuration successful.", hostLabel)
	return nil, nil
}

// taskDeleteHostOnFailure deletes a host
func (c *cluster) taskDeleteHostOnFailure(task concurrency.Task, params concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// Convert and validate params
	rh, ok := params.(resources.Host)
	if !ok || rh.IsNull() {
		return nil, fail.InvalidParameterError("params", "must be a valid 'resources.Host")
	}

	prefix := "Cleaning up on failure, "
	hostName := rh.GetName()
	logrus.Debugf(prefix + fmt.Sprintf("deleting Host '%s'", hostName))
	if xerr := rh.Delete(task); xerr != nil {
		logrus.Errorf(prefix + fmt.Sprintf("failed to delete Host '%s'", hostName))
		return nil, xerr
	}

	logrus.Debugf(prefix + fmt.Sprintf("successfully deleted Host '%s'", hostName))
	return nil, nil
}

type taskDeleteNodeParameters struct {
	node, master resources.Host
}

func (c *cluster) taskDeleteNode(task concurrency.Task, params concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// Convert and validate params
	p, ok := params.(taskDeleteNodeParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskDeleteNodeParameters'")
	}
	if p.node.IsNull() {
		return nil, fail.InvalidParameterError("params.node", "cannot be null value of 'resources.Host'")
	}
	if p.master.IsNull() {
		return nil, fail.InvalidParameterError("params.master", "cannot be null value of 'resources.Host'")
	}

	hostName := p.node.GetName()
	logrus.Debugf("Deleting Node '%s'", hostName)
	if xerr := c.deleteNode(task, p.node, p.master); xerr != nil {
		logrus.Errorf("Failed to delete Node '%s'", hostName)
		return nil, xerr
	}

	logrus.Debugf("Successfully deleted Node '%s'", hostName)
	return nil, nil
}

func (c *cluster) taskDeleteMaster(task concurrency.Task, params concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
	if c.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if task.IsNull() {
		return nil, fail.InvalidParameterError("task", "cannot be null value of 'concurrency.Task'")
	}

	// Convert and validate params
	p, ok := params.(taskDeleteNodeParameters)
	if !ok {
		return nil, fail.InvalidParameterError("params", "must be a 'taskDeleteNodeParameters'")
	}
	if p.master.IsNull() {
		return nil, fail.InvalidParameterError("params.master", "cannot be null value of 'resources.Host'")
	}

	hostName := p.master.GetName()
	logrus.Debugf("Deleting Master '%s'", hostName)
	if xerr := c.deleteMaster(task, p.master); xerr != nil {
		logrus.Errorf("Failed to delete Master '%s'", hostName)
		return nil, xerr
	}

	logrus.Debugf("Successfully deleted master '%s'", hostName)
	return nil, nil
}