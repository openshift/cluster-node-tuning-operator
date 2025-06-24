/*
 * Copyright 2024 Red Hat, Inc.
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

package environ

import (
	"log"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
)

type FS struct {
	Sys string
}

func DefaultFS() FS {
	return FS{
		Sys: "/sys",
	}
}

type Environ struct {
	DataPath string
	Root     FS
	Log      logr.Logger
}

func DefaultLog() logr.Logger {
	return stdr.New(log.New(os.Stderr, "", log.LstdFlags))
}

func New() *Environ {
	return &Environ{
		Root: DefaultFS(),
		Log:  DefaultLog(),
	}
}
