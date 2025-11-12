// Copyright (c) 2024 Wikimedia Foundation and contributors.
// All Rights Reserved.
//
// This file is part of Toolforge Builds-Api.
//
// Toolforge Builds-Api is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Toolforge Builds-Api is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with Toolforge Builds-Api. If not, see <http://www.gnu.org/licenses/>.
package internal

import (
	"regexp"
)

var BuildIdPrefix = "-buildpacks-pipelinerun-"
var BuildNamespace = "image-build"
var ToolforgeNameRegex = regexp.MustCompile("^[a-z]([-_a-z0-9]{0,254}[a-z0-9])?$")
var HarborNameRegex = regexp.MustCompile("^[a-z0-9]+(?:[._-][a-z0-9]+)*$")
var HarborProjectPrefix = "tool-"
var HarborSpecialCharFiller = "char.sep"
var HarborSpecialChars = "-_"
var ToolforgePrebuiltImagesProject = "toolforge-prebuilt-images"
var PipelineRunNotStartedErrorStr = "pipelineRun not yet started"
var ResourceNameEmptyErrorStr = "resource name may not be empty"
var PodInitializingErrorStr = "PodInitializing"
