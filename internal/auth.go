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

	return fmt.Errorf("user %s not allowed to act on build %s", user, buildId)
}
