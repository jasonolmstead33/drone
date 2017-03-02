package bitbucketserver

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/Sirupsen/logrus"

	"github.com/drone/drone/model"
	"github.com/drone/drone/remote/bitbucketserver/internal"
)

type triggerBody struct {
	Branch  string
	Event   string
	Author  string
	Commit  string
	Message string
}

func (t *triggerBody) String() string {
	return fmt.Sprintf("{\nBranch => %v\n Event => %v\n Author => %v \n Message => %v \n Commit => %v \n}",
		t.Branch,
		t.Event,
		t.Author,
		t.Message,
		t.Commit)
}

func parseTrigger(r *http.Request, baseURL string, name string, owner string) (*model.Repo, *model.Build, error) {
	decoder := json.NewDecoder(r.Body)
	var t triggerBody
	err := decoder.Decode(&t)
	if err != nil {
		log.Errorf("%v", err.Error())
	}
	defer r.Body.Close()

	build := convertTrigger(baseURL, name, owner, t)
	repo := &model.Repo{
		Name:     name,
		Owner:    owner,
		FullName: fmt.Sprintf("%s/%s", name, owner),
		Branch:   t.Branch,
		Kind:     model.RepoGit,
	}

	return repo, build, nil
}

// parseHook parses a Bitbucket hook from an http.Request request and returns
// Repo and Build detail. TODO: find a way to support PR hooks
func parseHook(r *http.Request, baseURL string) (*model.Repo, *model.Build, error) {
	hook := new(internal.PostHook)
	if err := json.NewDecoder(r.Body).Decode(hook); err != nil {
		return nil, nil, err
	}
	build := convertPushHook(hook, baseURL)
	repo := &model.Repo{
		Name:     hook.Repository.Slug,
		Owner:    hook.Repository.Project.Key,
		FullName: fmt.Sprintf("%s/%s", hook.Repository.Project.Key, hook.Repository.Slug),
		Branch:   "master",
		Kind:     model.RepoGit,
	}

	return repo, build, nil
}
