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

package operations

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/CS-SI/SafeScale/lib/protocol"
	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/iaas/objectstorage"
	"github.com/CS-SI/SafeScale/lib/server/iaas/userdata"
	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hostproperty"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hoststate"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/installmethod"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/ipversion"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/securitygroupstate"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/subnetproperty"
	"github.com/CS-SI/SafeScale/lib/server/resources/operations/converters"
	propertiesv1 "github.com/CS-SI/SafeScale/lib/server/resources/properties/v1"
	propertiesv2 "github.com/CS-SI/SafeScale/lib/server/resources/properties/v2"
	"github.com/CS-SI/SafeScale/lib/system"
	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/CS-SI/SafeScale/lib/utils/cli/enums/outputs"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/retry"
	"github.com/CS-SI/SafeScale/lib/utils/serialize"
	"github.com/CS-SI/SafeScale/lib/utils/strprocess"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

const (
	hostKind = "host"
	// hostsFolderName is the technical name of the container used to store networks info
	hostsFolderName = "hosts"

	// defaultHostSecurityGroupNamePattern = "safescale-sg_host_%s.%s.%s" // safescale-sg_host_<hostname>.<subnet name>.<network name>; should be unique across a tenant
)

// host ...
// follows interface resources.Host
type host struct {
	*core

	lock                          sync.RWMutex
	installMethods                map[uint8]installmethod.Enum
	privateIP, publicIP, accessIP string
	sshProfile                    *system.SSHConfig
}

// NewHost ...
func NewHost(svc iaas.Service) (_ resources.Host, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}

	coreInstance, xerr := newCore(svc, hostKind, hostsFolderName, &abstract.HostCore{})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	instance := &host{
		core: coreInstance,
		// lock: concurrency.NewTaskedLock(),
	}
	return instance, nil
}

// nullHost returns a *host corresponding to NullValue
func nullHost() *host {
	return &host{core: nullCore()}
}

// LoadHost ...
func LoadHost(svc iaas.Service, ref string) (rh resources.Host, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if svc == nil {
		return nullHost(), fail.InvalidParameterCannotBeNilError("svc")
	}
	if ref == "" {
		return nullHost(), fail.InvalidParameterCannotBeEmptyStringError("ref")
	}

	hostCache, xerr := svc.GetCache(hostKind)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nullHost(), xerr
	}

	options := []data.ImmutableKeyValue{
		data.NewImmutableKeyValue("onMiss", func() (cache.Cacheable, fail.Error) {
			rh, innerXErr := NewHost(svc)
			if innerXErr != nil {
				return nil, innerXErr
			}

			// TODO: core.ReadByID() does not check communication failure, side effect of limitations of Stow (waiting for stow replacement by rclone)
			if innerXErr = rh.Read(ref); innerXErr != nil {
				return nil, innerXErr
			}

			return rh, nil
		}),
	}

	ce, xerr := hostCache.Get(ref, options...)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// rewrite NotFoundError, user does not bother about metadata message
			return nullHost(), fail.NotFoundError("failed to find Host '%s'", ref)
		default:
			return nullHost(), xerr
		}
	}

	if rh = ce.Content().(resources.Host); rh == nil {
		return nil, fail.InconsistentError("nil value found in Host cache for key '%s'", ref)
	}
	_ = ce.LockContent()
	defer func() {
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			_ = ce.UnlockContent()
		}
	}()

	// deal with legacy
	xerr = rh.(*host).upgradeMetadataIfNeeded()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrAlteredNothing:
			// nothing changed, continue
		default:
			return nil, fail.Wrap(xerr, "failed to upgrade Host metadata")
		}
	}

	return rh, rh.(*host).updateCachedInformation()
}

// upgradeMetadataIfNeeded upgrades Host properties if needed
func (instance *host) upgradeMetadataIfNeeded() fail.Error {
	return instance.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
		if !props.Lookup(hostproperty.NetworkV2) {
			// -- get hostproperty.NetworkV1 --
			var hnV1 *propertiesv1.HostNetwork
			innerXErr := props.Alter(hostproperty.NetworkV1, func(clonable data.Clonable) fail.Error {
				var ok bool
				hnV1, ok = clonable.(*propertiesv1.HostNetwork)
				if !ok {
					return fail.InconsistentError("'*propertiesv1.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}
				return nil
			})
			if innerXErr != nil {
				return innerXErr
			}

			// -- load Network instance to apply metadata update if needed (creating missing Subnet metadata if needed) --
			var networkInstance resources.Network
			networkInstance, innerXErr = LoadNetwork(instance.GetService(), hnV1.DefaultNetworkID)
			if innerXErr != nil {
				return innerXErr
			}
			networkInstance.Released()

			// -- updates the Host metadata property NetworkV1 to NetworkV2
			innerXErr = props.Alter(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
				hnV2, ok := clonable.(*propertiesv2.HostNetworking)
				if !ok {
					return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				hnV2.DefaultSubnetID = hnV1.DefaultNetworkID
				hnV2.IPv4Addresses = hnV1.IPv4Addresses
				hnV2.IPv6Addresses = hnV1.IPv6Addresses
				hnV2.IsGateway = hnV1.IsGateway
				hnV2.PublicIPv4 = hnV1.PublicIPv4
				hnV2.PublicIPv6 = hnV1.PublicIPv6
				hnV2.SubnetsByID = hnV1.NetworksByID
				hnV2.SubnetsByName = hnV1.NetworksByName
				return nil
			})
			if innerXErr != nil {
				return innerXErr
			}

			// FIXME: clean old property or leave it ? will differ from v2 through time if Subnets are added for example
			return nil
		}

		return fail.AlteredNothingError()
	})
}

// updateCachedInformation loads in cache SSH configuration to access host; this information will not change over time
func (instance *host) updateCachedInformation() fail.Error {
	svc := instance.GetService()

	if len(instance.installMethods) == 0 {
		instance.installMethods = map[uint8]installmethod.Enum{}

		opUser, opUserErr := getOperatorUsernameFromCfg(svc)
		if opUserErr != nil {
			return opUserErr
		}

		return instance.Review(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
			ahc, ok := clonable.(*abstract.HostCore)
			if !ok {
				return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			var primaryGatewayConfig, secondaryGatewayConfig *system.SSHConfig
			innerXErr := props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
				hnV2, ok := clonable.(*propertiesv2.HostNetworking)
				if !ok {
					return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				if len(hnV2.IPv4Addresses) > 0 {
					instance.privateIP = hnV2.IPv4Addresses[hnV2.DefaultSubnetID]
					if instance.privateIP == "" {
						instance.privateIP = hnV2.IPv6Addresses[hnV2.DefaultSubnetID]
					}
				}
				instance.publicIP = hnV2.PublicIPv4
				if instance.publicIP == "" {
					instance.publicIP = hnV2.PublicIPv6
				}
				if instance.publicIP != "" {
					instance.accessIP = instance.publicIP
				} else {
					instance.accessIP = instance.privateIP
				}

				if !hnV2.Single && !hnV2.IsGateway {
					subnetInstance, xerr := LoadSubnet(svc, "", hnV2.DefaultSubnetID)
					xerr = debug.InjectPlannedFail(xerr)
					if xerr != nil {
						return xerr
					}

					rgw, xerr := subnetInstance.(*subnet).unsafeInspectGateway(true)
					xerr = debug.InjectPlannedFail(xerr)
					if xerr != nil {
						return xerr
					}

					gwErr := rgw.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
						gwahc, ok := clonable.(*abstract.HostCore)
						if !ok {
							return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
						}

						ip := rgw.(*host).accessIP
						primaryGatewayConfig = &system.SSHConfig{
							PrivateKey: gwahc.PrivateKey,
							Port:       int(gwahc.SSHPort),
							IPAddress:  ip,
							Hostname:   gwahc.Name,
							User:       opUser,
						}
						return nil
					})
					if gwErr != nil {
						return gwErr
					}

					// Secondary gateway may not exist...
					rgw, xerr = subnetInstance.(*subnet).unsafeInspectGateway(false)
					xerr = debug.InjectPlannedFail(xerr)
					if xerr != nil {
						switch xerr.(type) {
						case *fail.ErrNotFound:
							// continue
						default:
							return xerr
						}
					} else {
						gwErr = rgw.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
							gwahc, ok := clonable.(*abstract.HostCore)
							if !ok {
								return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
							}

							secondaryGatewayConfig = &system.SSHConfig{
								PrivateKey: gwahc.PrivateKey,
								Port:       int(gwahc.SSHPort),
								IPAddress:  rgw.(*host).accessIP,
								Hostname:   rgw.GetName(),
								User:       opUser,
							}
							return nil
						})
						if gwErr != nil {
							return gwErr
						}
					}
				}
				return nil
			})
			if innerXErr != nil {
				return innerXErr
			}

			instance.sshProfile = &system.SSHConfig{
				Port:                   int(ahc.SSHPort),
				IPAddress:              instance.accessIP,
				Hostname:               instance.GetName(),
				User:                   opUser,
				PrivateKey:             ahc.PrivateKey,
				GatewayConfig:          primaryGatewayConfig,
				SecondaryGatewayConfig: secondaryGatewayConfig,
			}

			var index uint8
			innerXErr = props.Inspect(hostproperty.SystemV1, func(clonable data.Clonable) fail.Error {
				systemV1, ok := clonable.(*propertiesv1.HostSystem)
				if !ok {
					logrus.Error(fail.InconsistentError("'*propertiesv1.HostSystem' expected, '%s' provided", reflect.TypeOf(clonable).String()))
				}
				if systemV1.Type == "linux" {
					switch systemV1.Flavor {
					case "centos", "redhat":
						index++
						instance.installMethods[index] = installmethod.Yum
					case "debian":
						fallthrough
					case "ubuntu":
						index++
						instance.installMethods[index] = installmethod.Apt
					case "fedora", "rhel":
						index++
						instance.installMethods[index] = installmethod.Dnf
					}
				}
				return nil
			})
			if innerXErr != nil {
				return innerXErr
			}

			index++
			instance.installMethods[index] = installmethod.Bash
			index++
			instance.installMethods[index] = installmethod.None
			return nil
		})
	}

	return nil
}

func getOperatorUsernameFromCfg(svc iaas.Service) (string, fail.Error) {
	cfg, xerr := svc.GetConfigurationOptions()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return "", xerr
	}

	var userName string
	if anon, ok := cfg.Get("OperatorUsername"); ok {
		userName = anon.(string)
		if userName == "" {
			logrus.Warnf("OperatorUsername is empty, check your tenants.toml file. Using 'safescale' user instead.")
		}
	}
	if userName == "" {
		userName = abstract.DefaultUser
	}

	return userName, nil
}

// IsNull tests if instance is nil or empty
func (instance *host) IsNull() bool {
	return instance.isNull()
}

// isNull tests if instance is nil or empty
func (instance *host) isNull() bool {
	return instance == nil || instance.core == nil || instance.core.isNull()
}

// carry ...
func (instance *host) carry(clonable data.Clonable) (xerr fail.Error) {
	if clonable == nil {
		return fail.InvalidParameterCannotBeNilError("clonable")
	}
	identifiable, ok := clonable.(data.Identifiable)
	if !ok {
		return fail.InvalidParameterError("clonable", "must also satisfy interface 'data.Identifiable'")
	}

	kindCache, xerr := instance.GetService().GetCache(instance.core.kind)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	xerr = kindCache.ReserveEntry(identifiable.GetID())
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}
	defer func() {
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			if derr := kindCache.FreeEntry(identifiable.GetID()); derr != nil {
				_ = xerr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to free %s cache entry for key '%s'", instance.core.kind, identifiable.GetID()))
			}

		}
	}()

	// Note: do not validate parameters, this call will do it
	xerr = instance.core.carry(clonable)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	cacheEntry, xerr := kindCache.CommitEntry(identifiable.GetID(), instance)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	cacheEntry.LockContent()

	return nil
}

// Browse walks through host folder and executes a callback for each entries
func (instance *host) Browse(ctx context.Context, callback func(*abstract.HostCore) fail.Error) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if callback == nil {
		return fail.InvalidParameterCannotBeNilError("callback")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.core.BrowseFolder(func(buf []byte) (innerXErr fail.Error) {
		if task.Aborted() {
			return fail.AbortedError(nil, "aborted")
		}

		ahc := abstract.NewHostCore()
		if innerXErr = ahc.Deserialize(buf); innerXErr != nil {
			return innerXErr
		}

		return callback(ahc)
	})
}

// ForceGetState returns the current state of the provider host after reloading metadata
func (instance *host) ForceGetState(ctx context.Context) (state hoststate.Enum, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	state = hoststate.Unknown
	if instance.isNull() {
		return state, fail.InvalidInstanceError()
	}
	if ctx == nil {
		return state, fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return state, xerr
	}

	if task.Aborted() {
		return state, fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	xerr = instance.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
		ahc, ok := clonable.(*abstract.HostCore)
		if !ok {
			return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		state = ahc.LastState
		return nil

	})
	return state, xerr
}

// Reload reloads host from metadata and current host state on provider state
func (instance *host) Reload() (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	xerr = instance.core.Reload()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *retry.ErrTimeout: // If retry timed out, log it and return error ErrNotFound
			xerr = fail.NotFoundError("metadata of host '%s' not found; host deleted?", instance.GetName())
		default:
			return xerr
		}
	}

	// Request host inspection from provider
	ahf, xerr := instance.GetService().InspectHost(instance.GetID())
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	// Updates the host metadata
	xerr = instance.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
		ahc, ok := clonable.(*abstract.HostCore)
		if !ok {
			return fail.InconsistentError("'*abstract.HostCore' expected, '%s' received", reflect.TypeOf(clonable).String())
		}

		changed := false
		if ahc.LastState != ahf.CurrentState {
			ahc.LastState = ahf.CurrentState
			changed = true
		}

		innerXErr := props.Alter(hostproperty.SizingV1, func(clonable data.Clonable) fail.Error {
			hostSizingV1, ok := clonable.(*propertiesv1.HostSizing)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostSizing' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			allocated := converters.HostEffectiveSizingFromAbstractToPropertyV1(ahf.Sizing)
			// FIXME: how to compare the 2 structs ?
			if allocated != hostSizingV1.AllocatedSize {
				hostSizingV1.AllocatedSize = allocated
				changed = true
			}
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Updates host property propertiesv1.HostNetworking
		innerXErr = props.Alter(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hnV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			_ = hnV2.Replace(converters.HostNetworkingFromAbstractToPropertyV2(*ahf.Networking))
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}
		if !changed {
			return fail.AlteredNothingError()
		}
		return nil
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrAlteredNothing:
			return nil
		default:
			return xerr
		}
	}

	return instance.updateCachedInformation()
}

// GetState returns the last known state of the host, without forced inspect
func (instance *host) GetState() (state hoststate.Enum) {
	state = hoststate.Unknown
	if instance.isNull() {
		return state
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	_ = instance.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
		ahc, ok := clonable.(*abstract.HostCore)
		if !ok {
			return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		state = ahc.LastState
		return nil
	})
	return state
}

// Create creates a new host and its metadata
// If the metadata is already carrying a host, returns fail.ErrNotAvailable
func (instance *host) Create(ctx context.Context, hostReq abstract.HostRequest, hostDef abstract.HostSizingRequirements) (_ *userdata.Content, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}
	if ctx == nil {
		return nil, fail.InvalidParameterCannotBeNilError("ctx")
	}
	hostname := instance.GetName()
	if hostname != "" {
		return nil, fail.NotAvailableError("already carrying host '%s'", hostname)
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	if task.Aborted() {
		return nil, fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(%s)", hostReq.ResourceName).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	svc := instance.GetService()

	// Check if host exists and is managed bySafeScale
	hostInstance, xerr := LoadHost(svc, hostReq.ResourceName)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
		// continue
		default:
			return nil, fail.Wrap(xerr, "failed to check if host '%s' already exists", hostReq.ResourceName)
		}
	} else {
		hostInstance.Released()
		return nil, fail.DuplicateError("'%s' already exists", hostReq.ResourceName)
	}

	// Check if host exists but is not managed by SafeScale
	_, xerr = svc.InspectHost(abstract.NewHostCore().SetName(hostReq.ResourceName))
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// continue
		default:
			return nil, fail.Wrap(xerr, "failed to check if host resource name '%s' is already used", hostReq.ResourceName)
		}
	} else {
		return nil, fail.DuplicateError("found an existing Host named '%s' (but not managed by SafeScale)", hostReq.ResourceName)
	}

	// If TemplateID is not explicitly provided, search the appropriate template to satisfy 'hostDef'
	if hostReq.TemplateID == "" {
		if hostDef.Template != "" {
			tmpl, xerr := svc.FindTemplateByName(hostDef.Template)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				switch xerr.(type) {
				case *fail.ErrNotFound:
				// continue
				default:
					return nil, xerr
				}
			} else {
				hostReq.TemplateID = tmpl.ID
			}
		}
	}
	if hostReq.TemplateID == "" {
		hostReq.TemplateID, xerr = instance.findTemplateID(hostDef)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, xerr
		}
	}

	// If hostReq.ImageID is not explicitly defined, find an image ID corresponding to the content of hostDef.Image
	if hostReq.ImageID == "" {
		hostReq.ImageID, xerr = instance.findImageID(&hostDef)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, fail.Wrap(xerr, "failed to find image to use on compute resource")
		}
	}

	// identify default Subnet
	var (
		defaultSubnet                  resources.Subnet
		undoCreateSingleHostNetworking func() fail.Error
	)
	if hostReq.Single {
		defaultSubnet, undoCreateSingleHostNetworking, xerr = createSingleHostNetworking(ctx, svc, hostReq)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, xerr
		}

		defer func() {
			if xerr != nil && !hostReq.KeepOnFailure {
				derr := undoCreateSingleHostNetworking()
				if derr != nil {
					_ = xerr.AddConsequence(derr)
				}
			}
		}()

		xerr = defaultSubnet.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
			as, ok := clonable.(*abstract.Subnet)
			if !ok {
				return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			hostReq.Subnets = append(hostReq.Subnets, as)
			hostReq.SecurityGroupIDs = map[string]struct{}{
				as.PublicIPSecurityGroupID: {},
				as.GWSecurityGroupID:       {},
			}
			hostReq.PublicIP = true
			return nil
		})
		if xerr != nil {
			return nil, xerr
		}
	} else {
		// By convention, default subnet is the first of the list
		as := hostReq.Subnets[0]
		defaultSubnet, xerr = LoadSubnet(svc, "", as.ID)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, xerr
		}

		if hostReq.DefaultRouteIP == "" {
			hostReq.DefaultRouteIP = func() string { out, _ := defaultSubnet.(*subnet).unsafeGetDefaultRouteIP(); return out }()
		}

		// list IDs of Security Groups to apply to Host
		if len(hostReq.SecurityGroupIDs) == 0 {
			hostReq.SecurityGroupIDs = make(map[string]struct{}, len(hostReq.Subnets)+1)
			for _, v := range hostReq.Subnets {
				hostReq.SecurityGroupIDs[v.InternalSecurityGroupID] = struct{}{}
			}

			opts, xerr := svc.GetConfigurationOptions()
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return nil, xerr
			}

			anon, ok := opts.Get("UseNATService")
			useNATService := ok && anon.(bool)
			if hostReq.PublicIP || useNATService {
				xerr = defaultSubnet.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
					as, ok := clonable.(*abstract.Subnet)
					if !ok {
						return fail.InconsistentError("*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
					}

					if as.PublicIPSecurityGroupID != "" {
						hostReq.SecurityGroupIDs[as.PublicIPSecurityGroupID] = struct{}{}
					}
					return nil
				})
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return nil, fail.Wrap(xerr, "failed to consult details of Subnet '%s'", defaultSubnet.GetName())
				}
			}
		}
	}
	defaultSubnetID := defaultSubnet.GetID()

	ahf, userdataContent, xerr := svc.CreateHost(hostReq)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		if _, ok := xerr.(*fail.ErrInvalidRequest); ok {
			return nil, xerr
		}
		return nil, fail.Wrap(xerr, "failed to create Host '%s'", hostReq.ResourceName)
	}

	defer func() {
		if xerr != nil && !hostReq.KeepOnFailure {
			if derr := svc.DeleteHost(ahf.Core.ID); derr != nil {
				_ = xerr.AddConsequence(fail.Wrap(derr, "cleaning up on %s, failed to delete Host '%s'", actionFromError(xerr), ahf.Core.Name))
			}
		}
	}()

	// Make sure ssh port wanted is set
	if hostReq.SSHPort > 0 {
		ahf.Core.SSHPort = hostReq.SSHPort
	} else {
		ahf.Core.SSHPort = 22
	}

	// Creates metadata early to "reserve" host name
	xerr = instance.carry(ahf.Core)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	defer func() {
		if xerr != nil && !hostReq.KeepOnFailure {
			if derr := instance.core.delete(); derr != nil {
				logrus.Errorf("cleaning up on %s, failed to delete host '%s' metadata: %v", actionFromError(xerr), ahf.Core.Name, derr)
				_ = xerr.AddConsequence(derr)
			}
		}
	}()

	xerr = instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		innerXErr := props.Alter(hostproperty.SizingV1, func(clonable data.Clonable) fail.Error {
			hostSizingV1, ok := clonable.(*propertiesv1.HostSizing)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSizing' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			hostSizingV1.AllocatedSize = converters.HostEffectiveSizingFromAbstractToPropertyV1(ahf.Sizing)
			hostSizingV1.RequestedSize = converters.HostSizingRequirementsFromAbstractToPropertyV1(hostDef)
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Sets host extension DescriptionV1
		innerXErr = props.Alter(hostproperty.DescriptionV1, func(clonable data.Clonable) fail.Error {
			hostDescriptionV1, ok := clonable.(*propertiesv1.HostDescription)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostDescription' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			_ = hostDescriptionV1.Replace(converters.HostDescriptionFromAbstractToPropertyV1(*ahf.Description))
			creator := ""
			hostname, _ := os.Hostname()
			if curUser, err := user.Current(); err == nil {
				creator = curUser.Username
				if hostname != "" {
					creator += "@" + hostname
				}
				if curUser.Name != "" {
					creator += " (" + curUser.Name + ")"
				}
			} else {
				creator = "unknown@" + hostname
			}
			hostDescriptionV1.Creator = creator
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Updates host property propertiesv2.HostNetworking
		return props.Alter(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hnV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			_ = hnV2.Replace(converters.HostNetworkingFromAbstractToPropertyV2(*ahf.Networking))
			hnV2.DefaultSubnetID = defaultSubnetID
			hnV2.IsGateway = hostReq.IsGateway
			hnV2.Single = hostReq.Single
			hnV2.PublicIPv4 = ahf.Networking.PublicIPv4
			hnV2.PublicIPv6 = ahf.Networking.PublicIPv6
			hnV2.SubnetsByID = ahf.Networking.SubnetsByID
			hnV2.SubnetsByName = ahf.Networking.SubnetsByName
			hnV2.IPv4Addresses = ahf.Networking.IPv4Addresses
			hnV2.IPv6Addresses = ahf.Networking.IPv6Addresses
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	xerr = instance.updateCachedInformation()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	xerr = instance.setSecurityGroups(ctx, hostReq, defaultSubnet)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}
	defer instance.undoSetSecurityGroups(&xerr, hostReq.KeepOnFailure)

	logrus.Infof("Compute resource created: '%s'", instance.GetName())

	// A host claimed ready by a Cloud provider is not necessarily ready
	// to be used until ssh service is up and running. So we wait for it before
	// claiming host is created
	logrus.Infof("Waiting SSH availability on Host '%s' ...", instance.GetName())

	// FIXME: configurable timeout here
	status, xerr := instance.waitInstallPhase(ctx, userdata.PHASE1_INIT, time.Duration(0))
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrTimeout:
			return nil, fail.Wrap(xerr, "timeout after Host creation waiting for SSH availability")
		default:
			if abstract.IsProvisioningError(xerr) {
				logrus.Errorf("%+v", xerr)
				return nil, fail.Wrap(xerr, "error provisioning the new host, please check safescaled logs", instance.GetName())
			}
			return nil, xerr
		}
	}

	xerr = instance.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
		// update host system property
		return props.Alter(hostproperty.SystemV1, func(clonable data.Clonable) fail.Error {
			systemV1, ok := clonable.(*propertiesv1.HostSystem)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSystem' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			parts := strings.Split(status, ",")
			systemV1.Type = parts[1]
			systemV1.Flavor = parts[2]
			systemV1.Image = hostReq.ImageID
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// -- Updates host link with subnets --
	xerr = instance.updateSubnets(task, hostReq)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}
	defer func() {
		instance.undoUpdateSubnets(hostReq, &xerr)
	}()

	xerr = instance.finalizeProvisioning(ctx, userdataContent, hostReq.Single)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	logrus.Infof("host '%s' created successfully", instance.GetName())
	return userdataContent, nil
}

// setSecurityGroups sets the Security Groups for the host
func (instance *host) setSecurityGroups(ctx context.Context, req abstract.HostRequest, defaultSubnet resources.Subnet) fail.Error {
	if req.Single {
		svc := instance.GetService()
		hostID := instance.GetID()
		for k := range req.SecurityGroupIDs {
			if k != "" {
				xerr := svc.BindSecurityGroupToHost(k, hostID)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}
			}
		}
		return nil
	}

	return instance.properties.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) (innerXErr fail.Error) {
		hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
		if !ok {
			return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		svc := instance.GetService()

		// get default Subnet core data
		var (
			as              *abstract.Subnet
			defaultSubnetID string
		)
		innerXErr = defaultSubnet.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
			var ok bool
			as, ok = clonable.(*abstract.Subnet)
			if !ok {
				return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			defaultSubnetID = as.ID
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		var (
			gwsg, pubipsg, lansg resources.SecurityGroup
		)

		// Apply Security Group for gateways in default Subnet
		if req.IsGateway {
			if gwsg, innerXErr = LoadSecurityGroup(svc, as.GWSecurityGroupID); innerXErr != nil {
				return fail.Wrap(innerXErr, "failed to query Subnet '%s' Security Group '%s'", defaultSubnet.GetName(), as.GWSecurityGroupID)
			}
			if innerXErr = gwsg.BindToHost(ctx, instance, resources.SecurityGroupEnable, resources.MarkSecurityGroupAsSupplemental); innerXErr != nil {
				return fail.Wrap(innerXErr, "failed to apply Subnet's Security Group for gateway '%s' on host '%s'", gwsg.GetName(), req.ResourceName)
			}

			defer func() {
				if innerXErr != nil && !req.KeepOnFailure {
					if derr := gwsg.UnbindFromHost(context.Background(), instance); derr != nil {
						_ = innerXErr.AddConsequence(fail.Wrap(derr, "cleaning up on %s, failed to unbind Security Group '%s' from Host '%s'", actionFromError(innerXErr), gwsg.GetName(), instance.GetName()))
					}
				}
			}()

			item := &propertiesv1.SecurityGroupBond{
				ID:         gwsg.GetID(),
				Name:       gwsg.GetName(),
				Disabled:   false,
				FromSubnet: true,
			}
			hsgV1.ByID[item.ID] = item
			hsgV1.ByName[item.Name] = item.ID
		}

		// Apply Security Group for hosts with public IP in default Subnet
		if req.IsGateway {
			if pubipsg, innerXErr = LoadSecurityGroup(svc, as.PublicIPSecurityGroupID); innerXErr != nil {
				return fail.Wrap(innerXErr, "failed to query Subnet '%s' Security Group with ID %s", defaultSubnet.GetName(), as.PublicIPSecurityGroupID)
			}
			defer pubipsg.Released()

			if innerXErr = pubipsg.BindToHost(ctx, instance, resources.SecurityGroupEnable, resources.MarkSecurityGroupAsSupplemental); innerXErr != nil {
				return fail.Wrap(innerXErr, "failed to apply Subnet's Security Group for gateway '%s' on host '%s'", pubipsg.GetName(), req.ResourceName)
			}

			defer func() {
				if innerXErr != nil && !req.KeepOnFailure {
					if derr := pubipsg.UnbindFromHost(context.Background(), instance); derr != nil {
						_ = innerXErr.AddConsequence(fail.Wrap(derr, "cleaning up on %s, failed to unbind Security Group '%s' from Host '%s'", actionFromError(innerXErr), pubipsg.GetName(), instance.GetName()))
					}
				}
			}()

			item := &propertiesv1.SecurityGroupBond{
				ID:         pubipsg.GetID(),
				Name:       pubipsg.GetName(),
				Disabled:   false,
				FromSubnet: true,
			}
			hsgV1.ByID[item.ID] = item
			hsgV1.ByName[item.Name] = item.ID
		}

		// Apply internal Security Group of each other subnets
		if req.IsGateway {
			defer func() {
				if innerXErr != nil && !req.KeepOnFailure {
					var (
						sg     resources.SecurityGroup
						derr   error
						errors []error
					)
					for _, v := range req.Subnets {
						if v.ID == defaultSubnetID {
							continue
						}

						subnetInstance, deeperXErr := LoadSubnet(svc, "", v.ID)
						if deeperXErr != nil {
							_ = innerXErr.AddConsequence(deeperXErr)
							continue
						}

						//goland:noinspection ALL
						defer func(item resources.Subnet) {
							item.Released()
						}(subnetInstance)

						sgName := sg.GetName()
						deeperXErr = subnetInstance.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
							as, ok := clonable.(*abstract.Subnet)
							if !ok {
								return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
							}

							if sg, derr = LoadSecurityGroup(svc, as.InternalSecurityGroupID); derr == nil {
								derr = sg.UnbindFromHost(context.Background(), instance)
								sg.Released()
							}
							if derr != nil {
								errors = append(errors, derr)
							}
							return nil
						})
						if deeperXErr != nil {
							_ = innerXErr.AddConsequence(fail.Wrap(deeperXErr, "cleaning up on failure, failed to unbind Security Group '%s' from Host", sgName))
						}
					}
					if len(errors) > 0 {
						_ = innerXErr.AddConsequence(fail.Wrap(fail.NewErrorList(errors), "failed to unbind Subnets Security Group from host '%s'", sg.GetName(), req.ResourceName))
					}
				}
			}()

			for _, v := range req.Subnets {
				// Do not try to bind defaultSubnet on gateway, because this code is running under a lock on defaultSubnet in this case, and this will lead to deadlock
				// (binding of gateway on defaultSubnet is done inside Subnet.Create() call)
				if req.IsGateway && v.ID == defaultSubnetID {
					continue
				}

				subnetInstance, xerr := LoadSubnet(svc, "", v.ID)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}
				//goland:noinspection ALL
				defer func(subnetInstance resources.Subnet) {
					subnetInstance.Released()
				}(subnetInstance)

				xerr = subnetInstance.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
					as, ok := clonable.(*abstract.Subnet)
					if !ok {
						return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
					}

					if lansg, innerXErr = LoadSecurityGroup(svc, as.InternalSecurityGroupID); innerXErr != nil {
						return fail.Wrap(innerXErr, "failed to load Subnet '%s' internal Security Group %s", as.Name, as.InternalSecurityGroupID)
					}
					defer func(sgInstance resources.SecurityGroup) {
						sgInstance.Released()
					}(lansg)

					if innerXErr = lansg.BindToHost(ctx, instance, resources.SecurityGroupEnable, resources.MarkSecurityGroupAsSupplemental); innerXErr != nil {
						return fail.Wrap(innerXErr, "failed to apply Subnet '%s' internal Security Group '%s' to host '%s'", as.Name, lansg.GetName(), req.ResourceName)
					}
					return nil
				})
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}

				// register security group in properties
				item := &propertiesv1.SecurityGroupBond{
					ID:         lansg.GetID(),
					Name:       lansg.GetName(),
					Disabled:   false,
					FromSubnet: true,
				}
				hsgV1.ByID[item.ID] = item
				hsgV1.ByName[item.Name] = item.ID
			}
		}

		var an *abstract.Network
		//		networkInstance, xerr := defaultSubnet.(*subnet).unsafeInspectNetwork()
		networkInstance, xerr := defaultSubnet.InspectNetwork()
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}
		defer networkInstance.Released()

		innerXErr = networkInstance.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
			var ok bool
			an, ok = clonable.(*abstract.Network)
			if !ok {
				return fail.InconsistentError("'*abstract.Network' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			return nil
		})
		if innerXErr != nil {
			return fail.Wrap(innerXErr, "failed to query Network of Subnet '%s'", defaultSubnet.GetName())
		}

		// Unbind "default" Security Group from Host if it is bound
		if sgName := svc.GetDefaultSecurityGroupName(); sgName != "" {
			adsg, innerXErr := svc.InspectSecurityGroupByName(an.ID, sgName)
			if innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrNotFound:
					// ignore this error
				default:
					return innerXErr
				}
			} else if innerXErr = svc.UnbindSecurityGroupFromHost(adsg, instance.GetID()); innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrNotFound:
					// Consider a security group not found as a successful unbind
				default:
					return fail.Wrap(innerXErr, "failed to unbind Security Group '%s' from Host", sgName)
				}
			}
		}

		return nil
	})
}

func (instance *host) undoSetSecurityGroups(errorPtr *fail.Error, keepOnFailure bool) {
	if errorPtr == nil {
		logrus.Errorf("trying to call a cancel function from a nil error; cancel not run")
		return
	}
	if *errorPtr != nil && !keepOnFailure {
		svc := instance.GetService()
		derr := instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
			return props.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) (innerXErr fail.Error) {
				hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
				if !ok {
					return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				var (
					opXErr fail.Error
					sg     resources.SecurityGroup
					errors []error
				)

				// unbind security groups
				for _, v := range hsgV1.ByName {
					if sg, opXErr = LoadSecurityGroup(svc, v); opXErr == nil {
						opXErr = sg.UnbindFromHost(context.Background(), instance)
					}
					if opXErr != nil {
						errors = append(errors, opXErr)
					}
				}
				if len(errors) > 0 {
					return fail.Wrap(fail.NewErrorList(errors), "cleaning up on %s, failed to unbind Security Groups from Host", actionFromError(*errorPtr))
				}

				return nil
			})
		})
		if derr != nil {
			_ = (*errorPtr).AddConsequence(fail.Wrap(derr, "cleaning up on %s, failed to cleanup Security Groups", actionFromError(*errorPtr)))
		}
	}
}

func (instance *host) findTemplateID(hostDef abstract.HostSizingRequirements) (string, fail.Error) {
	svc := instance.GetService()
	if hostDef.Template != "" {
		if tpl, xerr := svc.FindTemplateByName(hostDef.Template); xerr == nil {
			return tpl.ID, nil
		}
		logrus.Warning(fail.NotFoundError("failed to find template '%s', trying to guess from sizing...", hostDef.Template))
	}

	template, xerr := svc.FindTemplateBySizing(hostDef)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return "", xerr
	}

	return template.ID, nil
}

func (instance *host) findImageID(hostDef *abstract.HostSizingRequirements) (string, fail.Error) {
	svc := instance.GetService()
	if hostDef.Image == "" {
		cfg, xerr := svc.GetConfigurationOptions()
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return "", xerr
		}
		hostDef.Image = cfg.GetString("DefaultImage")
	}

	var img *abstract.Image
	xerr := retry.WhileUnsuccessfulDelay1Second(
		func() error {
			var innerXErr fail.Error
			img, innerXErr = svc.SearchImage(hostDef.Image)
			return innerXErr
		},
		30*time.Second,
	)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return "", xerr
	}
	return img.ID, nil
}

// runInstallPhase uploads then starts script corresponding to phase 'phase'
func (instance *host) runInstallPhase(ctx context.Context, phase userdata.Phase, userdataContent *userdata.Content) fail.Error {
	content, xerr := userdataContent.Generate(phase)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	file := fmt.Sprintf("%s/user_data.%s.sh", utils.TempFolder, phase)
	xerr = instance.unsafePushStringToFile(ctx, string(content), file)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	command := fmt.Sprintf("sudo bash %s; exit $?", file)
	// Executes the script on the remote host
	retcode, _, stderr, xerr := instance.unsafeRun(ctx, command, outputs.COLLECT, 0, 0)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return fail.Wrap(xerr, "failed to apply configuration phase '%s'", phase)
	}
	if retcode != 0 {
		if retcode == 255 {
			return fail.NewError("failed to execute install phase '%s' on host '%s': SSH connection failed", phase, instance.GetName())
		}
		return fail.NewError("failed to execute install phase '%s' on host '%s': %s", phase, instance.GetName(), stderr)
	}
	return nil
}

func (instance *host) waitInstallPhase(ctx context.Context, phase userdata.Phase, timeout time.Duration) (string, fail.Error) {
	sshDefaultTimeout := int(temporal.GetHostTimeout().Minutes())
	if sshDefaultTimeoutCandidate := os.Getenv("SSH_TIMEOUT"); sshDefaultTimeoutCandidate != "" {
		if num, err := strconv.Atoi(sshDefaultTimeoutCandidate); err == nil {
			logrus.Debugf("Using custom timeout of %d minutes", num)
			sshDefaultTimeout = num
		}
	}

	// TODO: configurable timeout here
	duration := time.Duration(sshDefaultTimeout) * time.Minute
	status, xerr := instance.sshProfile.WaitServerReady(ctx, string(phase), time.Duration(sshDefaultTimeout)*time.Minute)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrTimeout:
			return status, fail.Wrap(xerr.Cause(), "failed to wait for SSH on Host '%s' to be ready after %s (phase %s): %s", instance.GetName(), temporal.FormatDuration(duration), phase, status)
		default:
		}
		if abstract.IsProvisioningError(xerr) {
			logrus.Errorf("%+v", xerr)
			return status, fail.Wrap(xerr, "error provisioning Host '%s', please check safescaled logs", instance.GetName())
		}
	}
	return status, xerr
}

// updateSubnets updates subnets on which host is attached and host property HostNetworkV2
func (instance *host) updateSubnets(task concurrency.Task, req abstract.HostRequest) fail.Error {
	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	// If host is a gateway or is single, do not add it as host attached to the Subnet, it's considered as part of the subnet
	if !req.IsGateway && !req.Single {
		return instance.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
			return props.Alter(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
				hnV2, ok := clonable.(*propertiesv2.HostNetworking)
				if !ok {
					return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				hostID := instance.GetID()
				hostName := instance.GetName()

				for _, as := range req.Subnets {
					rs, innerXErr := LoadSubnet(instance.core.GetService(), "", as.ID)
					if innerXErr != nil {
						return innerXErr
					}

					innerXErr = rs.Alter(func(clonable data.Clonable, properties *serialize.JSONProperties) fail.Error {
						return properties.Alter(subnetproperty.HostsV1, func(clonable data.Clonable) fail.Error {
							subnetHostsV1, ok := clonable.(*propertiesv1.SubnetHosts)
							if !ok {
								return fail.InconsistentError("'*propertiesv1.SubnetHosts' expected, '%s' provided", reflect.TypeOf(clonable).String())
							}

							subnetHostsV1.ByName[hostName] = hostID
							subnetHostsV1.ByID[hostID] = hostName
							return nil
						})
					})
					if innerXErr != nil {
						return innerXErr
					}

					hnV2.SubnetsByID[as.ID] = as.Name
					hnV2.SubnetsByName[as.Name] = as.ID
				}
				return nil
			})
		})
	}
	return nil
}

// undoUpdateSubnets removes what updateSubnets have done
func (instance *host) undoUpdateSubnets(req abstract.HostRequest, errorPtr *fail.Error) {
	if errorPtr != nil && *errorPtr != nil && !req.IsGateway && !req.Single && !req.KeepOnFailure {
		// // Without this,the undo will not be able to complete in case it's called on an abort...
		// defer task.DisarmAbortSignal()()

		xerr := instance.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
			return props.Alter(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
				hsV1, ok := clonable.(*propertiesv2.HostNetworking)
				if !ok {
					return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				hostID := instance.GetID()
				hostName := instance.GetName()

				for _, as := range req.Subnets {
					rs, innerXErr := LoadSubnet(instance.core.GetService(), "", as.ID)
					if innerXErr != nil {
						return innerXErr
					}

					innerXErr = rs.Alter(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
						return props.Alter(subnetproperty.HostsV1, func(clonable data.Clonable) fail.Error {
							subnetHostsV1, ok := clonable.(*propertiesv1.SubnetHosts)
							if !ok {
								return fail.InconsistentError("'*propertiesv1.SubnetHosts' expected, '%s' provided", reflect.TypeOf(clonable).String())
							}

							delete(subnetHostsV1.ByID, hostID)
							delete(subnetHostsV1.ByName, hostName)
							return nil
						})
					})
					if innerXErr != nil {
						return innerXErr
					}

					delete(hsV1.SubnetsByID, as.ID)
					delete(hsV1.SubnetsByName, as.ID)
				}
				return nil
			})
		})
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			_ = (*errorPtr).AddConsequence(fail.Wrap(xerr, "cleaning up on %s, failed to remove Host relationships with Subnets", actionFromError(xerr)))
		}
	}
}

func (instance *host) finalizeProvisioning(ctx context.Context, userdataContent *userdata.Content, single bool) fail.Error {
	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	// Reset userdata script for Host from Cloud Provider metadata service (if stack is able to do so)
	xerr = instance.GetService().ClearHostStartupScript(instance.GetID())
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	// Executes userdata.PHASE2_NETWORK_AND_SECURITY script to configure networking and security
	xerr = instance.runInstallPhase(ctx, userdata.PHASE2_NETWORK_AND_SECURITY, userdataContent)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	// Update Keypair of the Host with the final one
	xerr = instance.Alter(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
		ah, ok := clonable.(*abstract.HostCore)
		if !ok {
			return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		ah.PrivateKey = userdataContent.FinalPrivateKey
		return nil
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return fail.Wrap(xerr, "failed to update Keypair")
	}
	xerr = instance.updateCachedInformation()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	logrus.Infof("finalizing host provisioning of '%s': rebooting", instance.GetName())
	// Reboot host
	command := "sudo systemctl reboot"
	_, _, _, _ = instance.unsafeRun(ctx, command, outputs.COLLECT, 10*time.Second, 30*time.Second)

	_, xerr = instance.waitInstallPhase(ctx, userdata.PHASE2_NETWORK_AND_SECURITY, 0)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	// if host is not a gateway, executes userdata.PHASE4/5 scripts
	// to fix possible system issues and finalize host creation.
	// For a gateway, userdata.PHASE3 to 5 have to be run explicitly (cf. operations/subnet.go)
	if !userdataContent.IsGateway {
		// execute userdata.PHASE4_SYSTEM_FIXES script to fix possible misconfiguration in system
		xerr = instance.runInstallPhase(ctx, userdata.PHASE4_SYSTEM_FIXES, userdataContent)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}

		// Reboot host
		logrus.Infof("finalizing host provisioning of '%s' (not-gateway): rebooting", instance.GetName())
		command = "sudo systemctl reboot"
		_, _, _, _ = instance.unsafeRun(ctx, command, outputs.COLLECT, 10*time.Second, 30*time.Second)

		_, xerr = instance.waitInstallPhase(ctx, userdata.PHASE4_SYSTEM_FIXES, 0)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}

		// execute userdata.PHASE5_FINAL script to final install/configure of the host (no need to reboot)
		xerr = instance.runInstallPhase(ctx, userdata.PHASE5_FINAL, userdataContent)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}

		_, xerr = instance.waitInstallPhase(ctx, userdata.PHASE5_FINAL, temporal.GetHostTimeout())
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			switch xerr.(type) { //nolint
			case *fail.ErrTimeout:
				return fail.Wrap(xerr, "timeout creating a host")
			}
			if abstract.IsProvisioningError(xerr) {
				logrus.Errorf("%+v", xerr)
				return fail.Wrap(xerr, "error provisioning the new host, please check safescaled logs", instance.GetName())
			}
			return xerr
		}
	}
	return nil
}

// WaitSSHReady waits until SSH responds successfully
func (instance *host) WaitSSHReady(ctx context.Context, timeout time.Duration) (_ string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return "", fail.InvalidInstanceError()
	}
	if ctx == nil {
		return "", fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return "", xerr
	}

	if task.Aborted() {
		return "", fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.waitInstallPhase(ctx, userdata.PHASE5_FINAL, timeout)
}

// createSingleHostNetwork creates Single-Host Network and Subnet
func createSingleHostNetworking(ctx context.Context, svc iaas.Service, singleHostRequest abstract.HostRequest) (_ resources.Subnet, _ func() fail.Error, xerr fail.Error) {
	// Build network name
	cfg, xerr := svc.GetConfigurationOptions()
	if xerr != nil {
		return nil, nil, xerr
	}

	bucketName := cfg.GetString("MetadataBucketName")
	if bucketName == "" {
		return nil, nil, fail.InconsistentError("missing service configuration option 'MetadataBucketName'")
	}

	networkName := fmt.Sprintf("sfnet-%s", strings.Trim(bucketName, objectstorage.BucketNamePrefix+"-"))

	// Create network if needed
	networkInstance, xerr := LoadNetwork(svc, networkName)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			networkInstance, xerr = NewNetwork(svc)
			if xerr != nil {
				return nil, nil, xerr
			}

			request := abstract.NetworkRequest{
				Name:          networkName,
				CIDR:          abstract.SingleHostNetworkCIDR,
				KeepOnFailure: true,
			}
			xerr = networkInstance.Create(ctx, request)
			if xerr != nil {
				return nil, nil, xerr
			}
		default:
			return nil, nil, xerr
		}
	}
	defer networkInstance.Released()

	// Check if Subnet exists
	var (
		subnetRequest abstract.SubnetRequest
		cidrIndex     uint
	)
	subnetInstance, xerr := LoadSubnet(svc, networkInstance.GetID(), singleHostRequest.ResourceName)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			subnetInstance, xerr = NewSubnet(svc)
			if xerr != nil {
				return nil, nil, xerr
			}

			var (
				subnetCIDR string
			)

			subnetCIDR, cidrIndex, xerr = reserveCIDRForSingleHost(networkInstance)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return nil, nil, xerr
			}

			var dnsServers []string
			opts, xerr := svc.GetConfigurationOptions()
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				switch xerr.(type) {
				case *fail.ErrNotFound:
				default:
					return nil, nil, xerr
				}
			} else {
				if servers := strings.TrimSpace(opts.GetString("DNSServers")); servers != "" {
					dnsServers = strings.Split(servers, ",")
				}
			}

			subnetRequest.Name = singleHostRequest.ResourceName
			subnetRequest.NetworkID = networkInstance.GetID()
			subnetRequest.IPVersion = ipversion.IPv4
			subnetRequest.CIDR = subnetCIDR
			subnetRequest.DNSServers = dnsServers
			subnetRequest.HA = false

			subnetInstance.(*subnet).lock.Lock()
			xerr = subnetInstance.(*subnet).createSubnetWithoutGateway(ctx, subnetRequest)
			subnetInstance.(*subnet).lock.Unlock()
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return nil, nil, xerr
			}

			defer func() {
				if xerr != nil && !singleHostRequest.KeepOnFailure {
					derr := subnetInstance.Delete(ctx)
					if derr != nil {
						_ = xerr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to delete Subnet '%s'", singleHostRequest.ResourceName))
					}
				}
			}()

			// Sets the CIDR index in instance metadata
			xerr = subnetInstance.Alter(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
				as, ok := clonable.(*abstract.Subnet)
				if !ok {
					return fail.InconsistentError("'*abstract.Subnet' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				as.SingleHostCIDRIndex = cidrIndex
				return nil
			})
			if xerr != nil {
				return nil, nil, xerr
			}
		default:
			return nil, nil, xerr
		}
	} else {
		subnetInstance.Released()
		return nil, nil, fail.DuplicateError("there is already a Subnet named '%s'", singleHostRequest.ResourceName)
	}

	undoFunc := func() fail.Error {
		var errors []error
		if !singleHostRequest.KeepOnFailure {
			derr := subnetInstance.Delete(ctx)
			if derr != nil {
				errors = append(errors, fail.Wrap(derr, "cleaning up on failure, failed to delete Subnet '%s'", singleHostRequest.ResourceName))
			}
			derr = freeCIDRForSingleHost(networkInstance, cidrIndex)
			if derr != nil {
				errors = append(errors, fail.Wrap(derr, "cleaning up on failure, failed to free CIDR slot in Network '%s'", networkInstance.GetName()))
			}
		}
		if len(errors) > 0 {
			return fail.NewErrorList(errors)
		}
		return nil
	}

	return subnetInstance, undoFunc, nil
}

// Delete deletes a host with its metadata and updates subnet links
func (instance *host) Delete(ctx context.Context) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	xerr = instance.Inspect(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
		// Do not remove a host that is a gateway
		return props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hostNetworkV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			if hostNetworkV2.IsGateway {
				return fail.NotAvailableError("cannot delete host, it's a gateway that can only be deleted through its Subnet")
			}
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	return instance.relaxedDeleteHost(ctx)
}

// relaxedDeleteHost is the method that really deletes a host, being a gateway or not
func (instance *host) relaxedDeleteHost(ctx context.Context) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	svc := instance.GetService()
	var shares map[string]*propertiesv1.HostShare
	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		// Do not remove a host having shares that are currently remotely mounted
		innerXErr := props.Inspect(hostproperty.SharesV1, func(clonable data.Clonable) fail.Error {
			sharesV1, ok := clonable.(*propertiesv1.HostShares)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostShares' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			shares = sharesV1.ByID
			shareCount := len(shares)
			for _, hostShare := range shares {
				count := len(hostShare.ClientsByID)
				if count > 0 {
					// clients found, checks if these clients already exists...
					for _, hostID := range hostShare.ClientsByID {
						instance, inErr := LoadHost(svc, hostID)
						if inErr == nil {
							instance.Released()
							return fail.NotAvailableError("host '%s' exports %d share%s and at least one share is mounted", instance.GetName(), shareCount, strprocess.Plural(uint(shareCount)))
						}
					}
				}
			}
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Do not delete a Host with Volumes attached
		return props.Inspect(hostproperty.VolumesV1, func(clonable data.Clonable) fail.Error {
			hostVolumesV1, ok := clonable.(*propertiesv1.HostVolumes)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostVolumes' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			nAttached := len(hostVolumesV1.VolumesByID)
			if nAttached > 0 {
				return fail.NotAvailableError("host '%s' has %d volume%s attached", instance.GetName(), nAttached, strprocess.Plural(uint(nAttached)))
			}
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	var (
		single         bool
		singleSubnetID string
	)
	xerr = instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		// If Host has mounted shares, unmounts them before anything else
		var mounts []*propertiesv1.HostShare
		innerXErr := props.Inspect(hostproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			hostMountsV1, ok := clonable.(*propertiesv1.HostMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			for _, i := range hostMountsV1.RemoteMountsByPath {
				if task.Aborted() {
					return fail.AbortedError(nil, "aborted")
				}

				// Retrieve v data
				shareInstance, loopErr := LoadShare(svc, i.ShareID)
				if loopErr != nil {
					return loopErr
				}

				//goland:noinspection ALL
				defer func(item resources.Share) {
					item.Released()
				}(shareInstance)

				// Retrieve data about the server serving the v
				rhServer, loopErr := shareInstance.GetServer()
				if loopErr != nil {
					return loopErr
				}

				// Retrieve data about v from its server
				item, loopErr := rhServer.GetShare(i.ShareID)
				if loopErr != nil {
					return loopErr
				}

				mounts = append(mounts, item)
			}
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Unmounts tier shares mounted on host (done outside the previous host.properties.Reading() section, because
		// Unmount() have to lock for write, and won't succeed while host.properties.Reading() is running,
		// leading to a deadlock)
		for _, v := range mounts {
			if task.Aborted() {
				return fail.AbortedError(nil, "aborted")
			}

			shareInstance, loopErr := LoadShare(svc, v.ID)
			if loopErr != nil {
				return loopErr
			}

			//goland:noinspection ALL
			defer func(item resources.Share) {
				item.Released()
			}(shareInstance)

			loopErr = shareInstance.Unmount(ctx, instance)
			if loopErr != nil {
				return loopErr
			}
		}

		// if host exports shares, delete them
		for _, v := range shares {
			if task.Aborted() {
				return fail.AbortedError(nil, "aborted")
			}

			shareInstance, loopErr := LoadShare(svc, v.Name)
			if loopErr != nil {
				return loopErr
			}

			loopErr = shareInstance.Delete(ctx)
			if loopErr != nil {
				return loopErr
			}
		}

		// Walk through property propertiesv1.HostNetworking to remove the reference to the host in Subnets
		innerXErr = props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hostNetworkV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}
			hostID := instance.GetID()
			// hostName := instance.GetName()

			single = hostNetworkV2.Single
			if single {
				singleSubnetID = hostNetworkV2.DefaultSubnetID
			}

			if !single {
				var errors []error
				for k := range hostNetworkV2.SubnetsByID {
					if !hostNetworkV2.IsGateway && k != hostNetworkV2.DefaultSubnetID {
						subnetInstance, loopErr := LoadSubnet(svc, "", k)
						if loopErr == nil {
							//goland:noinspection ALL
							defer func(item resources.Subnet) {
								item.Released()
							}(subnetInstance)

							loopErr = subnetInstance.AbandonHost(ctx, hostID)
						}
						if loopErr != nil {
							logrus.Errorf(loopErr.Error())
							errors = append(errors, loopErr)
							continue
						}
					}
				}
				if len(errors) > 0 {
					return fail.Wrap(fail.NewErrorList(errors), "failed to update metadata for Subnets of Host")
				}
			}
			return nil
		})
		if innerXErr != nil {
			return innerXErr
		}

		// Unbind Security Groups from Host
		innerXErr = props.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			// Unbind Security Groups from Host
			var errors []error
			for _, v := range hsgV1.ByID {
				rsg, derr := LoadSecurityGroup(svc, v.ID)
				if derr == nil {
					//goland:noinspection ALL
					defer func(sgInstance resources.SecurityGroup) {
						sgInstance.Released()
					}(rsg)

					derr = rsg.UnbindFromHost(ctx, instance)
				}
				if derr != nil {
					switch derr.(type) {
					case *fail.ErrNotFound:
						// Consider that a Security Group that cannot be loaded or is not bound as a success
					default:
						errors = append(errors, derr)
					}
				}
			}
			if len(errors) > 0 {
				return fail.Wrap(fail.NewErrorList(errors), "failed to unbind some Security Groups")
			}

			return nil
		})
		if innerXErr != nil {
			return fail.Wrap(innerXErr, "failed to unbind Security Groups from Host")
		}

		// Delete host
		waitForDeletion := true
		innerXErr = retry.WhileUnsuccessfulDelay1Second(
			func() error {
				if derr := svc.DeleteHost(instance.GetID()); derr != nil {
					switch derr.(type) {
					case *fail.ErrNotFound:
						// A host not found is considered as a successful deletion
						logrus.Tracef("host not found, deletion considered as a success")
					default:
						return fail.Wrap(derr, "cannot delete host")
					}
					waitForDeletion = false
				}
				return nil
			},
			time.Minute*5, // FIXME: hardcoded timeout
		)
		if innerXErr != nil {
			return innerXErr
		}

		// wait for effective host deletion
		if waitForDeletion {
			innerXErr = retry.WhileUnsuccessfulDelay5SecondsTimeout(
				func() error {
					state, stateErr := svc.GetHostState(instance.GetID())
					if stateErr != nil {
						switch stateErr.(type) {
						case *fail.ErrNotFound:
							// If host is not found anymore, consider this as a success
							return nil
						default:
							return stateErr
						}
					}
					if state == hoststate.Error {
						return fail.NotAvailableError("host is in state Error")
					}
					return nil
				},
				time.Minute*2, // FIXME: hardcoded duration
			)
			if innerXErr != nil {
				switch innerXErr.(type) {
				case *retry.ErrStopRetry:
					innerXErr = fail.ConvertError(innerXErr.Cause())
				default:
				}
			}
			if innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrNotFound:
					// continue
				default:
					return innerXErr
				}
			}
		}

		return nil
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if single {
		// delete its dedicated Subnet
		singleSubnetInstance, xerr := LoadSubnet(svc, "", singleSubnetID)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}

		xerr = singleSubnetInstance.Delete(ctx)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return xerr
		}
	}

	// Deletes metadata from Object Storage
	xerr = instance.core.delete()
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		// If entry not found, considered as a success
		if _, ok := xerr.(*fail.ErrNotFound); !ok {
			return xerr
		}
		logrus.Tracef("core instance not found, deletion considered as a success")
	}

	return nil
}

// GetSSHConfig loads SSH configuration for host from metadata
//
// FIXME: verify that system.SSHConfig carries data about secondary getGateway
func (instance *host) GetSSHConfig() (_ *system.SSHConfig, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.sshProfile, nil
}

// Run tries to execute command 'cmd' on the host
func (instance *host) Run(ctx context.Context, cmd string, outs outputs.Enum, connectionTimeout, executionTimeout time.Duration) (_ int, _ string, _ string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return 0, "", "", fail.InvalidInstanceError()
	}
	if ctx == nil {
		return -1, "", "", fail.InvalidParameterCannotBeNilError("ctx")
	}
	if cmd == "" {
		return -1, "", "", fail.InvalidParameterError("cmd", "cannot be empty string")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return -1, "", "", xerr
	}

	if task.Aborted() {
		return 0, "", "", fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(cmd='%s', outs=%s)", outs.String()).Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafeRun(ctx, cmd, outs, connectionTimeout, executionTimeout)
}

// Pull downloads a file from Host
func (instance *host) Pull(ctx context.Context, target, source string, timeout time.Duration) (_ int, _ string, _ string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return 0, "", "", fail.InvalidInstanceError()
	}
	if ctx == nil {
		return 0, "", "", fail.InvalidParameterCannotBeNilError("ctx")
	}
	if target == "" {
		return 0, "", "", fail.InvalidParameterCannotBeEmptyStringError("target")
	}
	if source == "" {
		return 0, "", "", fail.InvalidParameterCannotBeEmptyStringError("source")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return -1, "", "", xerr
	}

	if task.Aborted() {
		return 0, "", "", fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(target=%s,source=%s)", target, source).Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	// FIXME: reintroduce timeout on ssh.
	// if timeout < temporal.GetHostTimeout() {
	// 	timeout = temporal.GetHostTimeout()
	// }
	var (
		retcode        int
		stdout, stderr string
	)
	xerr = retry.WhileUnsuccessfulDelay5Seconds(
		func() error {
			var innerXErr fail.Error
			if retcode, stdout, stderr, innerXErr = instance.sshProfile.Copy(ctx, target, source, false); innerXErr != nil {
				return innerXErr
			}
			switch retcode { //nolint
			case 1: // FIXME: Check errorcodes
				if strings.Contains(stdout, "lost connection") {
					return fail.NewError("lost connection, retrying...")
				}
			}
			return nil
		},
		2*timeout,
	)
	return retcode, stdout, stderr, xerr
}

// Push uploads a file to host
func (instance *host) Push(ctx context.Context, source, target, owner, mode string, timeout time.Duration) (_ int, _ string, _ string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return 0, "", "", fail.InvalidInstanceError()
	}
	if ctx == nil {
		return 0, "", "", fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return -1, "", "", xerr
	}

	if task.Aborted() {
		return 0, "", "", fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(source=%s, target=%s, owner=%s, mode=%s)", source, target, owner, mode).Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafePush(ctx, source, target, owner, mode, timeout)
}

// GetShare returns a clone of the propertiesv1.HostShare corresponding to share 'shareRef'
func (instance *host) GetShare(shareRef string) (_ *propertiesv1.HostShare, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	var (
		hostShare *propertiesv1.HostShare
		// ok        bool
	)
	err := instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.SharesV1, func(clonable data.Clonable) fail.Error {
			sharesV1, ok := clonable.(*propertiesv1.HostShares)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostShares' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			if item, ok := sharesV1.ByID[shareRef]; ok {
				hostShare = item.Clone().(*propertiesv1.HostShare)
				return nil
			}
			if item, ok := sharesV1.ByName[shareRef]; ok {
				hostShare = sharesV1.ByID[item].Clone().(*propertiesv1.HostShare)
				return nil
			}
			return fail.NotFoundError("share '%s' not found in server '%s' metadata", shareRef, instance.GetName())
		})
	})
	err = debug.InjectPlannedFail(err)
	if err != nil {
		return nil, err
	}

	return hostShare, nil
}

// GetVolumes returns information about volumes attached to the host
func (instance *host) GetVolumes() (_ *propertiesv1.HostVolumes, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafeGetVolumes()
}

// Start starts the host
func (instance *host) Start(ctx context.Context) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	hostName := instance.GetName()
	hostID := instance.GetID()

	svc := instance.GetService()
	xerr = svc.StartHost(hostID)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	xerr = retry.WhileUnsuccessfulDelay5Seconds(
		func() error {
			if task.Aborted() {
				return fail.AbortedError(nil, "aborted")
			}

			return svc.WaitHostState(hostID, hoststate.Started, temporal.GetHostTimeout())
		},
		5*time.Minute,
	)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrAborted:
			if cerr := fail.ConvertError(xerr.Cause()); cerr != nil {
				return cerr
			}
			return xerr
		case *retry.ErrTimeout:
			return fail.Wrap(xerr, "timeout waiting host '%s' to be started", hostName)
		default:
			return xerr
		}
	}
	return nil
}

// Stop stops the host
func (instance *host) Stop(ctx context.Context) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	hostName := instance.GetName()
	hostID := instance.GetID()

	svc := instance.GetService()
	xerr = svc.StopHost(hostID)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	xerr = retry.WhileUnsuccessfulDelay5Seconds(
		func() error {
			if task.Aborted() {
				return fail.AbortedError(nil, "aborted")
			}

			return svc.WaitHostState(hostID, hoststate.Stopped, temporal.GetHostTimeout())
		},
		// FIXME: hardcoded value
		5*time.Minute,
	)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrAborted:
			if cerr := fail.ConvertError(xerr.Cause()); cerr != nil {
				return cerr
			}
			return xerr
		case *retry.ErrTimeout:
			return fail.Wrap(xerr, "timeout waiting host '%s' to be stopped", hostName)
		default:
			return xerr
		}
	}
	return nil
}

// Reboot reboots the host
func (instance *host) Reboot(ctx context.Context) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	xerr = instance.Stop(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}
	return instance.Start(ctx)
}

// Resize ...
// not yet implemented
func (instance *host) Resize(ctx context.Context, hostSize abstract.HostSizingRequirements) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host")).WithStopwatch().Entering()
	defer tracer.Exiting()

	return fail.NotImplementedError("Host.Resize() not yet implemented")
}

// GetPublicIP returns the public IP address of the host
func (instance *host) GetPublicIP() (ip string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	ip = ""
	if instance.isNull() {
		return ip, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	if ip = instance.publicIP; ip == "" {
		return ip, fail.NotFoundError("no public IP associated with Host '%s'", instance.GetName())
	}
	return ip, nil
}

// GetPrivateIP returns the private IP of the host on its default Networking
func (instance *host) GetPrivateIP() (_ string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return "", fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.privateIP, nil
}

// GetPrivateIPOnSubnet returns the private IP of the host on its default Subnet
func (instance *host) GetPrivateIPOnSubnet(subnetID string) (ip string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	ip = ""
	if instance.isNull() {
		return ip, fail.InvalidInstanceError()
	}
	if subnetID = strings.TrimSpace(subnetID); subnetID == "" {
		return ip, fail.InvalidParameterError("subnetID", "cannot be empty string")
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hostNetworkV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}
			if ip, ok = hostNetworkV2.IPv4Addresses[subnetID]; !ok {
				return fail.InvalidRequestError("host '%s' does not have an IP address on subnet '%s'", instance.GetName(), subnetID)
			}
			return nil
		})
	})
	return ip, xerr
}

// GetAccessIP returns the IP to reach the host
func (instance *host) GetAccessIP() (ip string, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	ip = ""
	if instance.isNull() {
		return ip, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.accessIP, nil
}

// GetShares returns the information about the shares hosted by the host
func (instance *host) GetShares() (shares *propertiesv1.HostShares, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	shares = &propertiesv1.HostShares{}
	if instance.isNull() {
		return shares, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.SharesV1, func(clonable data.Clonable) fail.Error {
			hostSharesV1, ok := clonable.(*propertiesv1.HostShares)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostShares' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			shares = hostSharesV1
			return nil
		})
	})
	return shares, xerr
}

// GetMounts returns the information abouts the mounts of the host
func (instance *host) GetMounts() (mounts *propertiesv1.HostMounts, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	mounts = nil
	if instance.isNull() {
		return mounts, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafeGetMounts()
}

// IsClusterMember returns true if the host is member of a cluster
func (instance *host) IsClusterMember() (yes bool, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	yes = false
	if instance.isNull() {
		return yes, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.ClusterMembershipV1, func(clonable data.Clonable) fail.Error {
			hostClusterMembershipV1, ok := clonable.(*propertiesv1.HostClusterMembership)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostClusterMembership' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			yes = hostClusterMembershipV1.Cluster != ""
			return nil
		})
	})
	return yes, xerr
}

// IsGateway tells if the host acts as a gateway for a Subnet
func (instance *host) IsGateway() (_ bool, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return false, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	var state bool
	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hnV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}
			state = hnV2.IsGateway
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return false, xerr
	}

	return state, nil
}

// IsSingle tells if the host is single
func (instance *host) IsSingle() (_ bool, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return false, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	var state bool
	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.NetworkV2, func(clonable data.Clonable) fail.Error {
			hnV2, ok := clonable.(*propertiesv2.HostNetworking)
			if !ok {
				return fail.InconsistentError("'*propertiesv2.HostNetworking' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}
			state = hnV2.Single
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return false, xerr
	}

	return state, nil
}

// PushStringToFile creates a file 'filename' on remote 'host' with the content 'content'
func (instance *host) PushStringToFile(ctx context.Context, content string, filename string) (xerr fail.Error) {
	return instance.PushStringToFileWithOwnership(ctx, content, filename, "", "")
}

// PushStringToFileWithOwnership creates a file 'filename' on remote 'host' with the content 'content', and apply ownership
func (instance *host) PushStringToFileWithOwnership(ctx context.Context, content string, filename string, owner, mode string) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if content == "" {
		return fail.InvalidParameterError("content", "cannot be empty string")
	}
	if filename == "" {
		return fail.InvalidParameterError("filename", "cannot be empty string")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(content, filename='%s', ownner=%s, mode=%s", filename, owner, mode).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafePushStringToFileWithOwnership(ctx, content, filename, owner, mode)
}

// GetDefaultSubnet returns the Networking instance corresponding to host default subnet
func (instance *host) GetDefaultSubnet() (rs resources.Subnet, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nullSubnet(), fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.unsafeGetDefaultSubnet()
}

// ToProtocol convert an resources.Host to protocol.Host
func (instance *host) ToProtocol() (ph *protocol.Host, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	var (
		ahc           *abstract.HostCore
		hostSizingV1  *propertiesv1.HostSizing
		hostVolumesV1 *propertiesv1.HostVolumes
		volumes       []string
	)

	publicIP := instance.publicIP
	privateIP := instance.privateIP

	xerr = instance.Inspect(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
		var ok bool
		ahc, ok = clonable.(*abstract.HostCore)
		if !ok {
			return fail.InconsistentError("'*abstract.HostCore' expected, '%s' provided", reflect.TypeOf(clonable).String())
		}

		return props.Inspect(hostproperty.SizingV1, func(clonable data.Clonable) fail.Error {
			hostSizingV1, ok = clonable.(*propertiesv1.HostSizing)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSizing' expected, '%s' provided", reflect.TypeOf(clonable).String)
			}
			return props.Inspect(hostproperty.VolumesV1, func(clonable data.Clonable) fail.Error {
				hostVolumesV1, ok = clonable.(*propertiesv1.HostVolumes)
				if !ok {
					return fail.InconsistentError("'*propertiesv1.HostVolumes' expected, '%s' provided", reflect.TypeOf(clonable).String)
				}

				volumes = make([]string, 0, len(hostVolumesV1.VolumesByName))
				for _, v := range hostVolumesV1.VolumesByName {
					volumes = append(volumes, v)
				}
				return nil
			})
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return ph, xerr
	}

	ph = &protocol.Host{
		Cpu:                 int32(hostSizingV1.AllocatedSize.Cores),
		Disk:                int32(hostSizingV1.AllocatedSize.DiskSize),
		Id:                  ahc.ID,
		PublicIp:            publicIP,
		PrivateIp:           privateIP,
		Name:                ahc.Name,
		PrivateKey:          ahc.PrivateKey,
		Password:            ahc.Password,
		Ram:                 hostSizingV1.AllocatedSize.RAMSize,
		State:               protocol.HostState(ahc.LastState),
		AttachedVolumeNames: volumes,
	}
	return ph, nil
}

// BindSecurityGroup binds a security group to the host; if enabled is true, apply it immediately
func (instance *host) BindSecurityGroup(ctx context.Context, rsg resources.SecurityGroup, enable resources.SecurityGroupActivation) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if rsg == nil {
		return fail.InvalidParameterCannotBeNilError("rsg")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(rsg='%s', enable=%v", rsg.GetName(), enable).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	return instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			sgID := rsg.GetID()
			// If the Security Group is already bound to the host with the exact same state, consider as a success
			if v, ok := hsgV1.ByID[sgID]; ok && v.Disabled == !bool(enable) {
				return nil
			}

			// Not found, add it
			item := &propertiesv1.SecurityGroupBond{
				ID:       sgID,
				Name:     rsg.GetName(),
				Disabled: bool(!enable),
			}
			hsgV1.ByID[sgID] = item
			hsgV1.ByName[item.Name] = item.ID

			// If enabled, apply it
			if innerXErr := rsg.BindToHost(ctx, instance, enable, resources.MarkSecurityGroupAsSupplemental); innerXErr != nil {
				switch innerXErr.(type) {
				case *fail.ErrDuplicate:
					// already bound, success
				default:
					return innerXErr
				}
			}
			return nil
		})
	})
}

// UnbindSecurityGroup unbinds a security group from the host
func (instance *host) UnbindSecurityGroup(ctx context.Context, sg resources.SecurityGroup) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if sg == nil {
		return fail.InvalidParameterCannotBeNilError("sg")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	sgName := sg.GetName()
	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(sg='%s')", sgName).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	return instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			sgID := sg.GetID()
			// Check if the security group is listed for the host
			found := false
			for k, v := range hsgV1.ByID {
				if task.Aborted() {
					return fail.AbortedError(nil, "aborted")
				}

				if k == sgID {
					if v.FromSubnet {
						return fail.InvalidRequestError("cannot unbind Security Group '%s': inherited from Subnet", sgName)
					}
					found = true
					break
				}
			}
			// If not found, consider request successful
			if !found {
				return nil
			}

			// unbind security group from host on remote service side
			if innerXErr := sg.UnbindFromHost(ctx, instance); innerXErr != nil {
				return innerXErr
			}

			// found, delete it from properties
			delete(hsgV1.ByID, sgID)
			delete(hsgV1.ByName, sg.GetName())
			return nil
		})
	})
}

// ListSecurityGroups returns a slice of security groups binded to host
func (instance *host) ListSecurityGroups(state securitygroupstate.Enum) (list []*propertiesv1.SecurityGroupBond, xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	var emptySlice []*propertiesv1.SecurityGroupBond
	if instance.isNull() {
		return emptySlice, fail.InvalidInstanceError()
	}

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	xerr = instance.Inspect(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			list = filterBondsByKind(hsgV1.ByID, state)
			return nil
		})
	})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return emptySlice, xerr
	}

	return list, nil
}

// EnableSecurityGroup enables a bound security group to host by applying its rules
func (instance *host) EnableSecurityGroup(ctx context.Context, sg resources.SecurityGroup) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterError("ctx", "cannot be nil")
	}
	if sg == nil {
		return fail.InvalidParameterError("sg", "cannot be null value of 'SecurityGroup'")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	sgName := sg.GetName()
	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(sg='%s')", sgName).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	svc := instance.GetService()
	return instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			var asg *abstract.SecurityGroup
			xerr := sg.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
				var ok bool
				if asg, ok = clonable.(*abstract.SecurityGroup); !ok {
					return fail.InconsistentError("'*abstract.SecurityGroup' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				return nil
			})
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}

			// First check if the security group is not already registered for the host with the exact same state
			var found bool
			for k := range hsgV1.ByID {
				if task.Aborted() {
					return fail.AbortedError(nil, "aborted")
				}

				if k == asg.ID {
					found = true
					break
				}
			}
			if !found {
				return fail.NotFoundError("security group '%s' is not bound to host '%s'", sgName, instance.GetID())
			}

			if svc.GetCapabilities().CanDisableSecurityGroup {
				xerr = svc.EnableSecurityGroup(asg)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}
			} else {
				// Bind the security group on provider side; if already bound (*fail.ErrDuplicate), consider as a success
				xerr = sg.GetService().BindSecurityGroupToHost(asg, instance.GetID())
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					switch xerr.(type) {
					case *fail.ErrDuplicate:
						// continue
					default:
						return xerr
					}
				}
			}

			// found and updated, update metadata
			hsgV1.ByID[asg.ID].Disabled = false
			return nil
		})
	})
}

// DisableSecurityGroup disables a binded security group to host
func (instance *host) DisableSecurityGroup(ctx context.Context, rsg resources.SecurityGroup) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterError("ctx", "cannot be nil")
	}
	if rsg == nil {
		return fail.InvalidParameterError("rsg", "cannot be nil")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	sgName := rsg.GetName()
	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.host"), "(rsg='%s')", sgName).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.Lock()
	defer instance.lock.Unlock()

	svc := instance.GetService()
	return instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(hostproperty.SecurityGroupsV1, func(clonable data.Clonable) fail.Error {
			hsgV1, ok := clonable.(*propertiesv1.HostSecurityGroups)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostSecurityGroups' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			var asg *abstract.SecurityGroup
			xerr := rsg.Review(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
				var ok bool
				if asg, ok = clonable.(*abstract.SecurityGroup); !ok {
					return fail.InconsistentError("'*abstract.SecurityGroup' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}

				return nil
			})
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}

			// First check if the security group is not already registered for the host with the exact same state
			var found bool
			for k := range hsgV1.ByID {
				if task.Aborted() {
					return fail.AbortedError(nil, "aborted")
				}

				if k == asg.ID {
					found = true
					break
				}
			}
			if !found {
				return fail.NotFoundError("security group '%s' is not bound to host '%s'", sgName, rsg.GetID())
			}

			if svc.GetCapabilities().CanDisableSecurityGroup {
				xerr = svc.DisableSecurityGroup(asg)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}
			} else {
				// Bind the security group on provider side; if security group not binded, consider as a success
				xerr = svc.UnbindSecurityGroupFromHost(asg, instance.GetID())
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					switch xerr.(type) {
					case *fail.ErrNotFound:
						// continue
					default:
						return xerr
					}
				}
			}

			// found, update properties
			hsgV1.ByID[asg.ID].Disabled = true
			return nil
		})
	})
}
