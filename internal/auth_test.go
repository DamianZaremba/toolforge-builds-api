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
