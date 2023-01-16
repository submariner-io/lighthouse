/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package lighthouse

import (
	"fmt"

	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/go-logr/logr"
	loga "github.com/submariner-io/admiral/pkg/log"
)

type corednsLogSink struct {
	log clog.P
}

func (c *corednsLogSink) Init(_ logr.RuntimeInfo) {
}

func (c *corednsLogSink) Enabled(_ int) bool {
	return true
}

func (c *corednsLogSink) Info(level int, msg string, kvList ...interface{}) {
	for i := 0; i < len(kvList); i += 2 {
		s, ok := kvList[i].(string)
		if ok && s == loga.WarningKey {
			c.log.Warning(msg)
			return
		}
	}

	if level >= loga.DEBUG {
		c.log.Debug(msg)
		return
	}

	c.log.Info(msg)
}

func (c *corednsLogSink) Error(err error, msg string, kvList ...interface{}) {
	if err != nil {
		msg = fmt.Sprintf("%s error=%s", msg, err.Error())
	}

	for i := 0; i < len(kvList); i += 2 {
		s, ok := kvList[i].(string)
		if ok && s == loga.FatalKey {
			c.log.Fatal(msg)
			return
		}
	}

	c.log.Error(msg)
}

func (c *corednsLogSink) WithValues(_ ...interface{}) logr.LogSink {
	return c
}

func (c *corednsLogSink) WithName(name string) logr.LogSink {
	return &corednsLogSink{log: clog.NewWithPlugin(fmt.Sprintf("%s(%s)", PluginName, name))}
}
