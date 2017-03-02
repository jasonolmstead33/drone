package server

import (
	"fmt"
	"strconv"

	log "github.com/Sirupsen/logrus"
	"github.com/drone/drone/model"
	"github.com/drone/drone/remote"
	"github.com/drone/drone/shared/httputil"
	"github.com/drone/drone/store"
	"github.com/drone/drone/yaml"
	"github.com/gin-gonic/gin"
	"github.com/jasonolmstead33/mq/stomp"
	jose "github.com/square/go-jose"
)

//Trigger
func Trigger(c *gin.Context) {
	remote_ := remote.FromContext(c)

	owner := c.Param("owner")
	name := c.Param("name")
	tmprepo, build, err := remote_.Trigger(c.Request, name, owner)

	if err != nil {
		log.Errorf("%v", err.Error())
	}

	log.Debugf("Tmprepo => %v", tmprepo)
	log.Debugf("Context => %v", c)
	log.Debugf("Build => %v", build)

	log.Debugf("Owner => %v", owner)
	log.Debugf("Name => %v", name)

	repo, err := store.GetRepoOwnerName(c, owner, name)
	if err != nil {
		log.Errorf("failure to find repo %s/%s from hook. %s", owner, name, err)
		c.AbortWithError(404, err)
		return
	}

	log.Debugf("Repo => %v", repo)

	user, err := store.GetUser(c, repo.UserID)
	if err != nil {
		log.Errorf("failure to find repo owner %s. %s", repo.FullName, err)
		c.AbortWithError(500, err)
		return
	}

	log.Debugf("User => %v", user)

	config := ToConfig(c)
	raw, err := remote_.File(user, repo, build, config.Yaml)
	if err != nil {
		log.Errorf("failure to get build config for %s. %s", repo.FullName, err)
		c.AbortWithError(404, err)
		return
	}
	sec, err := remote_.File(user, repo, build, config.Shasum)
	if err != nil {
		log.Debugf("cannot find build secrets for %s. %s", repo.FullName, err)
		// NOTE we don't exit on failure. The sec file is optional
	}

	axes, err := yaml.ParseMatrix(raw)

	if err != nil {
		c.String(500, "Failed to parse yaml file or calculate matrix. %s", err)
		return
	}
	if len(axes) == 0 {
		axes = append(axes, yaml.Axis{})
	}

	netrc, err := remote_.Netrc(user, repo)
	if err != nil {
		c.String(500, "Failed to generate netrc file. %s", err)
		return
	}

	// verify the branches can be built vs skipped
	branches := yaml.ParseBranch(raw)
	if !branches.Match(build.Branch) && build.Event != model.EventTag && build.Event != model.EventDeploy {
		c.String(200, "Branch does not match restrictions defined in yaml")
		return
	}

	signature, err := jose.ParseSigned(string(sec))
	if err != nil {
		log.Debugf("cannot parse .drone.yml.sig file. %s", err)
	} else if len(sec) == 0 {
		log.Debugf("cannot parse .drone.yml.sig file. empty file")
	} else {
		build.Signed = true
		output, err := signature.Verify([]byte(repo.Hash))
		if err != nil {
			log.Debugf("cannot verify .drone.yml.sig file. %s", err)
		} else if string(output) != string(raw) {
			log.Debugf("cannot verify .drone.yml.sig file. no match")
		} else {
			build.Verified = true
		}
	}

	// update some build fields
	build.Status = model.StatusPending
	build.RepoID = repo.ID

	// and use a transaction
	var jobs []*model.Job
	for num, axis := range axes {
		jobs = append(jobs, &model.Job{
			BuildID:     build.ID,
			Number:      num + 1,
			Status:      model.StatusPending,
			Environment: axis,
		})
	}
	err = store.CreateBuild(c, build, jobs...)
	if err != nil {
		log.Errorf("failure to save commit for %s. %s", repo.FullName, err)
		c.AbortWithError(500, err)
		return
	}

	c.JSON(200, build)

	url := fmt.Sprintf("%s/%s/%d", httputil.GetURL(c.Request), repo.FullName, build.Number)
	err = remote_.Status(user, repo, build, url)
	if err != nil {
		log.Errorf("error setting commit status for %s/%d", repo.FullName, build.Number)
	}

	// get the previous build so that we can send
	// on status change notifications
	last, _ := store.GetBuildLastBefore(c, repo, build.Branch, build.ID)
	secs, err := store.GetMergedSecretList(c, repo)
	if err != nil {
		log.Debugf("Error getting secrets for %s#%d. %s", repo.FullName, build.Number, err)
	}

	client := stomp.MustFromContext(c)
	client.SendJSON("/topic/events", model.Event{
		Type:  model.Enqueued,
		Repo:  *repo,
		Build: *build,
	},
		stomp.WithHeader("repo", repo.FullName),
		stomp.WithHeader("private", strconv.FormatBool(repo.IsPrivate)),
	)

	for _, job := range jobs {
		broker, _ := stomp.FromContext(c)
		work := &model.Work{
			Signed:    build.Signed,
			Verified:  build.Verified,
			User:      user,
			Repo:      repo,
			Build:     build,
			BuildLast: last,
			Job:       job,
			Netrc:     netrc,
			Yaml:      string(raw),
			Secrets:   secs,
			System:    &model.System{Link: httputil.GetURL(c.Request)},
		}

		aq := getQueueString(raw, build.Event)
		log.Errorln("Event Type => %v", build.Event)
		log.Errorln("Sending message on queue, %v", aq)

		broker.SendJSON(aq,
			work,
			stomp.WithHeader(
				"platform",
				yaml.ParsePlatformDefault(raw, "linux/amd64"),
			),
			stomp.WithHeaders(
				yaml.ParseLabel(raw),
			),
		)
	}

}
