/*
 * Copyright 2018-2020, CS Systemes d'Information, http://www.c-s.fr
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

package remotefile

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/system"
	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/CS-SI/SafeScale/lib/utils/cli/enums/outputs"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/retry"
	"github.com/CS-SI/SafeScale/lib/utils/scerr"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// RemoteFileItem is a helper struct to ease the copy of local files to remote
type RemoteFileItem struct {
	Local        string
	Remote       string
	RemoteOwner  string
	RemoteRights string
}

// Upload transfers the local file to the hostname
func (rfc RemoteFileItem) Upload(task concurrency.Task, host resources.Host) error {
	if rfc.Local == "" {
		return scerr.InvalidInstanceContentError("rfc.Local", "cannot be empty string")
	}
	if rfc.Remote == "" {
		return scerr.InvalidInstanceContentError("rfc.Remote", "cannot be empty string")

	}
	if task == nil {
		return scerr.InvalidParameterError("task", "cannot be nil")
	}
	if host == nil {
		return scerr.InvalidParameterError("host", "cannot be nil")
	}

	tracer := concurrency.NewTracer(voidtask, "", true).WithStopwatch().GoingIn()
	defer tracer.OnExitTrace()()
	defer scerr.OnExitLogError(tracer.TraceMessage(""), &err)()

	retryErr := retry.WhileUnsuccessful(
		func() error {
			retcode, _, _, err := host.Push(task, rfc.Local, rfc.Remote, rfc.RemoteOwner, rfc.RemoteRights, temporal.GetExecutionTimeout())
			if err != nil {
				return err
			}
			if retcode != 0 {
				// If retcode == 1 (general copy error), retry. It may be a temporary network incident
				if retcode == 1 {
					// File may exist on target, try to remote it
					_, _, _, err = host.Run(voidtask, fmt.Sprintf("sudo rm -f %s", remotepath), temporal.GetBigDelay(), temporal.GetExecutionTimeout())
					if err == nil {
						return fmt.Errorf("file may exist on remote with inappropriate access rights, deleted it and retrying")
					}
					// If submission of removal of remote file fails, stop the retry and consider this as an unrecoverable network error
					return retry.StopRetryError("an unrecoverable network error has occurred", err)
				}
				if system.IsSCPRetryable(retcode) {
					err = fmt.Errorf("failed to copy file '%s' to '%s:%s' (retcode: %d=%s)", localpath, host.Name(), remotepath, retcode, system.SCPErrorString(retcode))
					return err
				}
				return nil
			}
			return nil
		},
		temporal.GetDefaultDelay(),
		temporal.GetLongOperationTimeout(),
	)
	if retryErr != nil {
		switch realErr := retryErr.(type) { // nolint
		case *retry.ErrStopRetry:
			return scerr.Wrap(realErr.Cause(), "failed to copy file to remote host '%s'", host.Name())
		case *retry.ErrTimeout:
			return scerr.Wrap(realErr, "timeout trying to copy file to '%s:%s'", host.Name(), remotepath)
		}
		return retryErr
	}

	// Updates owner and access rights if asked for
	cmd := ""
	if rfc.RemoteOwner != "" {
		cmd += "chown " + rfc.RemoteOwner + " " + rfc.Remote
	}
	if rfc.RemoteRights != "" {
		if cmd != "" {
			cmd += " && "
		}
		cmd += "chmod " + rfc.RemoteRights + " " + rfc.Remote
	}
	retcode, _, _, err = host.Run(task, cmd, outputs.COLLECT, temporal.GetConnectionTimeout(), temporal.GetExecutionTimeout())
	if err != nil {
		return err
	}
	if retcode != 0 {
		return fmt.Errorf("failed to update owner and/or access rights of the remote file")
	}

	return nil
}

// Upload transfers the local file to the hostname
func (rfc RemoteFileItem) UploadString(task concurrency.Task, content string, host resources.Host) error {
	if rfc.Remote == "" {
		return scerr.InvalidInstanceContentError("rfc.Remote", "cannot be empty string")

	}
	if task == nil {
		return scerr.InvalidParameterError("task", "cannot be nil")
	}

	if forensics := os.Getenv("SAFESCALE_FORENSICS"); forensics != "" {
		_ = os.MkdirAll(utils.AbsPathify(fmt.Sprintf("$HOME/.safescale/forensics/%s", host.Name())), 0777)
		partials := strings.Split(filename, "/")
		dumpName := utils.AbsPathify(fmt.Sprintf("$HOME/.safescale/forensics/%s/%s", host.Name(), partials[len(partials)-1]))

		err := ioutil.WriteFile(dumpName, []byte(content), 0644)
		if err != nil {
			logrus.Warnf("[TRACE] Forensics error creating %s", dumpName)
		}
	}

	f, err := system.CreateTempFileFromString(content, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %s", err.Error())
	}
	rfc.Local = f.Name()
	return rfc.Upload(task, host)
}

// RemoveRemote deletes the remote file from host
func (rfc RemoteFileItem) RemoveRemote(task concurrency.Task, host resources.Host) error {
	cmd := "rm -rf " + rfc.Remote
	retcode, _, _, err := host.Run(task, cmd, outputs.COLLECT, temporal.GetConnectionTimeout(), temporal.GetExecutionTimeout())
	if err != nil || retcode != 0 {
		return fmt.Errorf("failed to remove file '%s:%s'", host.Name(), rfc.Remote)
	}
	return nil
}

// RemoteFilesHandler handles the copy of files and cleanup
type RemoteFilesHandler struct {
	items []*RemoteFileItem
}

// Add adds a RemoteFileItem in the handler
func (rfh *RemoteFilesHandler) Add(file *RemoteFileItem) {
	rfh.items = append(rfh.items, file)
}

// Count returns the number of files in the handler
func (rfh *RemoteFilesHandler) Count() uint {
	return uint(len(rfh.items))
}

// Upload executes the copy of files
// TODO: allow to upload to many hosts
func (rfh *RemoteFilesHandler) Upload(task concurrency.Task, host resources.Host) error {
	for _, v := range rfh.items {
		err := v.Upload(task, host)
		if err != nil {
			return err
		}
	}
	return nil
}

// Cleanup executes the removal of remote files.
// NOTE: Removal of local files is the responsability of the caller, not the RemoteFilesHandler.
// TODO: allow to cleanup on many hosts
func (rfh *RemoteFilesHandler) Cleanup(task concurrency.Task, host resources.Host) {
	for _, v := range rfh.items {
		err := v.RemoveRemote(task, host)
		if err != nil {
			logrus.Warnf(err.Error())
		}
	}
}
