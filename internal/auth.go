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
	"fmt"
	"net/http"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

func subjectLineToUser(subjectLine string) (string, error) {
	commonNameRe := regexp.MustCompile("CN=([^,]+)")
	commonNameMatches := commonNameRe.FindStringSubmatch(subjectLine)
	// two matches, the whole regex and the group
	if len(commonNameMatches) != 2 {
		return "", fmt.Errorf("unable to parse cert subject line, wrong common name: %s\ngot matches %v", subjectLine, commonNameMatches)
	}

	organizationRe := regexp.MustCompile("O=([^,]+)")
	organizationMatches := organizationRe.FindStringSubmatch(subjectLine)
	// two matches, the whole regex and the group
	if len(organizationMatches) != 2 {
		return "", fmt.Errorf("unable to parse cert subject line, wrong organization: %s", subjectLine)
	}

	organizations := strings.Split(organizationMatches[1], " ")

	user := commonNameMatches[1]

	if len(organizations) != 1 || organizations[0] != "toolforge" && organizations[0] != "system:masters" {
		return user, fmt.Errorf("user %s of groups %v not authorized to access the bulidservice api", user, organizations)
	}

	return user, nil
}

func ValidateUser(subjectLine string) (string, error) {
	user, err := subjectLineToUser(subjectLine)
	log.Debugf("Got user %v, error %s", user, err)
	return user, err
}

func GetUserFromRequest(request *http.Request) (string, error) {
	clientSubjectLine := request.Header["Ssl-Client-Subject-Dn"]
	if len(clientSubjectLine) == 0 {
		return "", fmt.Errorf("got no authentication header")
	}
	user, err := ValidateUser(clientSubjectLine[0])
	if err != nil {
		return "", err
	}
	return user, nil
}

func ToolIsAllowedForBuild(user string, buildId string, buildIdPrefix string) error {
	if strings.HasPrefix(buildId, fmt.Sprintf("%s%s", user, buildIdPrefix)) {
		return nil
	}

	return fmt.Errorf("Build '%s' does not exist or belong to tool '%s'. Double check the name and try again.", buildId, user)
}
