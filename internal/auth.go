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
// along with Toolforge Builds-Api. If not, see <http://www.gnu.org/licenses/>
package internal

import (
	"fmt"
	"net/http"
	"strings"
)

func GetUserFromRequest(request *http.Request) (string, error) {
	// Currently we only care about the tool, not the user
	tool, ok := request.Header["X-Toolforge-Tool"]
	if !ok {
		// we got no auth, so no user
		return "", nil
	}
	if len(tool) != 1 {
		return "", fmt.Errorf("got an unexpected value for the tool authentication header: %v", tool)
	}
	return tool[0], nil
}

func ToolIsAllowedForBuild(user string, buildId string, buildIdPrefix string) error {
	if strings.HasPrefix(buildId, fmt.Sprintf("%s%s", user, buildIdPrefix)) {
		return nil
	}

	return fmt.Errorf("Build '%s' does not exist or belong to tool '%s'. Double check the name and try again.", buildId, user)
}
