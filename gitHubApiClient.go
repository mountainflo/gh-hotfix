package main

import (
	"context"
	"fmt"
	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
	"strconv"
	"strings"
	"time"
)

type gitHubApiClient struct {
	ctx       context.Context
	repoOwner string
	repoName  string
	client    *github.Client
}

func newGitHubApiClient(repoOwner, repoName, token string, ctx context.Context) *gitHubApiClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return &gitHubApiClient{
		ctx:       ctx,
		repoOwner: repoOwner,
		repoName:  repoName,
		client:    client,
	}
}

// getMergedPullRequests returns for a comma separated string of PRs all merged PRs
func (c *gitHubApiClient) getMergedPullRequests(pullRequests string) ([]*github.PullRequest, error) {
	var prs []*github.PullRequest
	for _, prNum := range strings.Split(strings.ReplaceAll(pullRequests, "#", ""), ",") {
		prInt, _ := strconv.Atoi(prNum)
		pr, _, err := c.client.PullRequests.Get(c.ctx, c.repoOwner, c.repoName, prInt)
		if err != nil {
			return nil, fmt.Errorf("error for fetching PR data from GitHub: %v", err)
		}
		if pr.GetMerged() {
			prs = append(prs, pr)
		} else {
			fmt.Printf("Non-merged PR #%v can't be added to hotfix. PR has state: %v\n", *pr.Number, *pr.State)
		}
	}
	return prs, nil
}

// collectCommitsFromPRs fetches for each PR all its commits
func (c *gitHubApiClient) collectCommitsFromPRs(prs []*github.PullRequest) (map[uint32]commitMatch, error) {
	hashToCommitMatch := make(map[uint32]commitMatch)
	for _, pr := range prs {
		commits, _, err := c.client.PullRequests.ListCommits(c.ctx, c.repoOwner, c.repoName, *pr.Number, nil)
		if err != nil {
			return nil, fmt.Errorf("can't retrieve commits for PR %s: %v", *pr.Number, err)
		}
		for _, c := range commits {
			wc := wrappedCommit{commit: c}
			hashToCommitMatch[wc.hash()] = commitMatch{
				prCommit: wc,
				pr:       pr,
			}
		}
	}
	return hashToCommitMatch, nil
}

// openPullRequest opens pull request
func (c *gitHubApiClient) openPullRequest(pull *github.NewPullRequest) (*github.PullRequest, error) {
	pr, _, err := c.client.PullRequests.Create(c.ctx, c.repoOwner, c.repoName, pull)
	if err != nil {
		return nil, err
	}
	return pr, nil
}

// fetchCommits fetch all commits for a branch since a given time
func (c *gitHubApiClient) fetchCommits(branchName string, since time.Time) ([]*github.RepositoryCommit, error) {
	commits, _, err := c.client.Repositories.ListCommits(c.ctx, c.repoOwner, c.repoName, &github.CommitsListOptions{
		SHA:   branchName,
		Since: since,
	})
	if err != nil {
		return nil, err
	}
	return commits, nil
}
