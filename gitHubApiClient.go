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
func (c *gitHubApiClient) getMergedPullRequests(pullRequests string) (*hotfixPrs, error) {
	var hotfixPrs hotfixPrs
	for _, prNum := range strings.Split(pullRequests, ",") {
		prInt, _ := strconv.Atoi(prNum)
		pr, _, err := c.client.PullRequests.Get(c.ctx, c.repoOwner, c.repoName, prInt)
		if err != nil {
			return nil, fmt.Errorf("error for fetching PR data from GitHub: %v", err)
		}
		if pr.GetMerged() {
			hotfixPrs.prs = append(hotfixPrs.prs, &wrappedPullRequest{pr: pr})
		} else {
			fmt.Printf("Non-merged PR #%v can't be added to hotfix. PR has state: %v\n", *pr.Number, *pr.State)
		}
	}
	return &hotfixPrs, nil
}

// collectCommitsFromPRs fetches for each PR all its commits
func (c *gitHubApiClient) collectCommitsFromPRs(hotfixPrs *hotfixPrs) error {
	allCommitMatches := make(map[uint32]*commitMatch)
	for _, pr := range (*hotfixPrs).prs {
		commits, _, err := c.client.PullRequests.ListCommits(c.ctx, c.repoOwner, c.repoName, *pr.pr.Number, nil)
		if err != nil {
			return fmt.Errorf("can't retrieve commits for PR %d: %v", *pr.pr.Number, err)
		}

		hashToCommitMatch := make(map[uint32]*commitMatch)
		var lastCommmitMatch *commitMatch
		for i, c := range commits {
			wc := wrappedCommit{commit: c}
			commitMatch := commitMatch{
				prCommit: wc,
			}

			// link commit matches
			if lastCommmitMatch != nil {
				commitMatch.previous = lastCommmitMatch
				lastCommmitMatch.next = &commitMatch
			}
			hashToCommitMatch[wc.hash()] = &commitMatch
			allCommitMatches[wc.hash()] = &commitMatch

			if i == 0 {
				pr.head = &commitMatch
			}

			lastCommmitMatch = &commitMatch
		}
		pr.tail = lastCommmitMatch
		pr.commits = hashToCommitMatch
	}
	hotfixPrs.allCommits = allCommitMatches
	return nil
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
