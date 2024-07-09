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
	"testing"
)

func TestGetUserFromRequestWorksWhenToolHeaderPassed(t *testing.T) {
	expectedUser := "dummy_user"
	gottenUser, err := GetUserFromRequest(&http.Request{
		Header: http.Header{"X-Toolforge-Tool": []string{expectedUser}},
	})

	if err != nil {
		t.Errorf("Expected no error, got %s", err)
	}

	if gottenUser != expectedUser {
		t.Errorf("Expected user %s, got %s", expectedUser, gottenUser)
	}
}

func TestGetUserFromRequestFailsWhenEmptyHeaderPassed(t *testing.T) {
	_, err := GetUserFromRequest(&http.Request{
		Header: http.Header{"X-Toolforge-Tool": []string{}},
	})

	if err == nil {
		t.Errorf("Expected an error, did not get any")
	}
}

func TestGetUserFromRequestFailsWhenManyValuesInHeaderPassed(t *testing.T) {
	_, err := GetUserFromRequest(&http.Request{
		Header: http.Header{"X-Toolforge-Tool": []string{"user_one", "user_two"}},
	})

	if err == nil {
		t.Errorf("Expected an error, did not get any")
	}
}

func TestGetUserFromRequestReturnsEmptyWhenNoHeaderPassed(t *testing.T) {
	expectedUser := ""
	gottenUser, err := GetUserFromRequest(&http.Request{
		Header: http.Header{},
	})

	if err != nil {
		t.Errorf("Expected no error, got %s", err)
	}

	if gottenUser != expectedUser {
		t.Errorf("Expected user %s, got %s", expectedUser, gottenUser)
	}
}

func TestToolIsAllowedForBuildFailsIfBuildIsNotPrefixedWithUser(t *testing.T) {
	err := ToolIsAllowedForBuild("user", "nouser-prefixed-buildId", BuildIdPrefix)
	if err == nil {
		t.Error("I was expecting an error.")
		t.Errorf("Expected an error, did not get any")
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
