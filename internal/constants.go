package internal

import (
	"regexp"
)

var BuildIdPrefix = "-buildpacks-pipelinerun-"
var BuildNamespace = "image-build"
var ToolforgeNameRegex = regexp.MustCompile("^[a-z]([-_a-z0-9]{0,254}[a-z0-9])?$")
var HarborNameRegex = regexp.MustCompile("^[a-z0-9]+(?:[._-][a-z0-9]+)*$")
var ToolforgeProjectPrefix = "tool-"
var HarborSpecialCharFiller = "char.sep"
var HarborSpecialChars = "-_"
