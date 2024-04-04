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
	"testing"
)

func TestValidateUserFailsWhenInvalidSubject(t *testing.T) {
	_, err := ValidateUser("dummy invalid subject line")

	if err == nil {
		t.Error("Expected an error for an invalid subject line.")
	}
}

func TestValidateUserFailsWhenNoOrg(t *testing.T) {
	_, err := ValidateUser("CN=dummyuserwithoutorg")

	if err == nil {
		t.Error("Expected an error for an invalid subject line.")
	}
}

func TestValidateUserFailsWhenNoOrgIsNotToolforge(t *testing.T) {
	_, err := ValidateUser("CN=dummyuser,O=nottoolforge")

	if err == nil {
		t.Error("Expected an error for an invalid subject line.")
	}
}

func TestValidateUserReturnsUserWhenOrgIsToolforge(t *testing.T) {
	expectedUser := "myuser"
	gottenUser, err := ValidateUser("CN=myuser,O=toolforge")

	if err != nil {
		t.Errorf("Expected no error but got: %s", err)
	}

	if gottenUser != expectedUser {
		t.Errorf("I was expecting user '%s', but got '%s'", expectedUser, gottenUser)
	}
}

func TestToolIsAllowedForBuildFailsIfBuildIsNotPrefixedWithUser(t *testing.T) {
	err := ToolIsAllowedForBuild("user", "nouser-prefixed-buildId", BuildIdPrefix)
	if err == nil {
		t.Error("I was expecting an error.")
	}
}

func TestToolIsAllowedForBuildFailsIfBuildIsPrefixedWithUserButNotBuildIdPrefix(t *testing.T) {
	err := ToolIsAllowedForBuild("user", "user-prefixed-buildId-without-BuildIdPrefix", BuildIdPrefix)
	if err == nil {
		t.Error("I was expecting an error.")
	}
}

func TestToolIsAllowedForBuildPassesIfBuildIsPrefixedWithUserAndBuildIdPrefix(t *testing.T) {
	err := ToolIsAllowedForBuild("user", fmt.Sprintf("user%sprefixed-buildId-with-BuildIdPrefix", BuildIdPrefix), BuildIdPrefix)
	if err != nil {
		t.Errorf("I was not expecting an error but got %s", err)
	}
}
