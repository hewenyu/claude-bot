package github

import "strings"

// AppKey names what c connects to, the repository: one repository is
// watched by one project.
func (c Config) AppKey() string {
	return Name + ":" + strings.ToLower(c.Repo)
}
