package main

import (
	"context"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
)

const (
	owner = "mountainflo"
	repo  = "gh-hotfix" // TODO get this info from current repo
)

type commitMatch struct {
	pr         *github.PullRequest
	prCommit   wrappedCommit
	mainCommit wrappedCommit
}

type wrappedCommit struct {
	commit *github.RepositoryCommit
}

// hash creates a hash value to easily identify a commit
// Commit SHAs can't be used as they change after rebase&merge of the PR is done
func (c wrappedCommit) hash() uint32 {
	v := c.commit.GetCommit()
	h := fnv.New32a()
	h.Write([]byte(v.GetMessage()))
	if v.GetAuthor() != nil {
		// For Author the date should be the same for the commit of the PR and the commit within main branch
		// For Commiter the date will be different as rebase&merge created a commit
		h.Write([]byte(fmt.Sprintf("%v", v.GetAuthor().GetDate())))
	} else {
		// problematic in case of duplicate commit messages
		fmt.Sprintf("WARN: no commitAuthor field. hash will only be generated from commit msg")
	}
	return h.Sum32()
}

func (c wrappedCommit) equal(other wrappedCommit) bool {
	a := c.commit.GetCommit()
	b := other.commit.GetCommit()
	return a.GetMessage() == b.GetMessage() &&
		a.GetStats().GetDeletions() == b.GetStats().GetDeletions() &&
		a.GetStats().GetAdditions() == b.GetStats().GetAdditions() &&
		a.GetStats().GetTotal() == b.GetStats().GetTotal()
}

func main() {

	mbUsage := "main branch where to cherry pick from"
	rbUsage := "release branch to add the hotfix to"
	hUsage := "name of the hotfix"
	prsUsage := "comma-separated list of PRs, e.g: '#42,#164'"

	var mainBranch string
	var releaseBranch string
	var hotfixName string
	var pullRequests string

	flag.StringVar(&mainBranch, "mainBranch", "master", mbUsage)
	flag.StringVar(&mainBranch, "mb", "master", mbUsage)
	flag.StringVar(&releaseBranch, "releaseBranch", "", rbUsage)
	flag.StringVar(&releaseBranch, "rb", "", rbUsage)
	flag.StringVar(&hotfixName, "hotfix", "", hUsage)
	flag.StringVar(&hotfixName, "hf", "", hUsage)
	flag.StringVar(&pullRequests, "pullRequests", "", prsUsage)
	flag.StringVar(&pullRequests, "prs", "", prsUsage)
	flag.StringVar(&pullRequests, "help", "", "help")
	flag.Parse()

	if releaseBranch == "" {
		fmt.Print("parameter 'releaseBranch' can't be empty")
		os.Exit(1)
	}
	if hotfixName == "" {
		fmt.Print("parameter 'hotfixName' can't be empty")
		os.Exit(1)
	}
	if pullRequests == "" {
		fmt.Print("parameter 'pullRequests' can't be empty")
		os.Exit(1)
	}

	token := getGitHubToken()
	if token == "" {
		fmt.Print("Error: GITHUB_TOKEN environment variable is not set")
		os.Exit(1)
	}

	fmt.Printf("Creating hotfix %s based on %s.\n", hotfixName, releaseBranch)
	fmt.Printf("Cherry-Picking commits of PRs '%s' from branch '%s'\n", pullRequests, mainBranch)

	ctx := context.Background()
	client := createGitHubClient(token, ctx)

	prs, err := getMergedPullRequests(pullRequests, client, ctx)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(prs) == 0 {
		fmt.Println("Empty PR List")
		os.Exit(1)
	}

	unmatchedPrCommits, err := collectCommitsFromPRs(prs, client, ctx)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// fetch commits from main branch
	mainBranchCommits, _, err := client.Repositories.ListCommits(ctx, owner, repo, &github.CommitsListOptions{
		SHA:   mainBranch,
		Since: getOldestPRCreationDate(prs),
	})
	if err != nil {
		fmt.Printf("Failed to list commits: %v\n", err)
		os.Exit(1)
	}

	matchingCommits, err := matchCommits(mainBranchCommits, unmatchedPrCommits)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	for _, commit := range matchingCommits {
		fmt.Printf("mainCommitSHA: %v; prCommitSHA: %v\n", commit.mainCommit.commit.GetSHA(), commit.prCommit.commit.GetSHA())
	}

	// checkout hotfix branch based on the release branch
	err = checkoutHotfixBranch(err, releaseBranch, hotfixName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// cherry pick commits to hotfix branch
	for _, c := range matchingCommits {
		commitSHA := c.mainCommit.commit.GetSHA()
		gitCherryPick := exec.Command("git", "cherry-pick", commitSHA)
		_, err = gitCherryPick.Output()
		if err != nil {
			fmt.Printf("Error during 'git cherry-pick %v': %s", commitSHA, err)
			os.Exit(1)
		}
	}

	// push hotfix branch
	gitPushHotfix := exec.Command("git", "push", hotfixName)
	_, err = gitPushHotfix.Output()
	if err != nil {
		fmt.Printf("error during 'git push %s': %v", hotfixName, err)
		os.Exit(1)
	}

	// open PR for hotfix and add a nice summary of the included PRs
	pr, _, err := client.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("Hotfix %v", hotfixName)),
		Head:  github.String(hotfixName),
		Base:  github.String(releaseBranch),
		Body:  github.String("BODY"),
	})
	if err != nil {
		fmt.Printf("Error creating pull request: %v\n", err)
		return
	}

	fmt.Printf("Successfully create PR: %s\n", pr.GetHTMLURL())
}

func checkoutHotfixBranch(err error, releaseBranch, hotfixName string) error {
	gitFetch := exec.Command("git", "fetch")
	_, err = gitFetch.Output()
	if err != nil {
		return fmt.Errorf("error during 'git fetch': %v", err)
	}

	gitCheckoutRelease := exec.Command("git", "checkout", releaseBranch)
	_, err = gitCheckoutRelease.Output()
	if err != nil {
		return fmt.Errorf("error during 'git checkout': %v", err)
	}

	gitPull := exec.Command("git", "pull")
	_, err = gitPull.Output()
	if err != nil {
		return fmt.Errorf("error during 'git pull': %v", err)
	}

	gitCheckoutHotfix := exec.Command("git", "checkout", "-b", hotfixName)
	_, err = gitCheckoutHotfix.Output()
	if err != nil {
		return fmt.Errorf("error during 'git checkout -b %s': %v", hotfixName, err)
	}

	return nil
}

func matchCommits(mainBranchCommits []*github.RepositoryCommit, unmatchedPrCommits map[uint32]commitMatch) ([]commitMatch, error) {
	var matchingCommits []commitMatch
	for _, commit := range mainBranchCommits {
		wrappedMainBranchCommit := wrappedCommit{commit: commit}

		commitMatch, ok := unmatchedPrCommits[wrappedMainBranchCommit.hash()]

		if ok {
			commitMatch.mainCommit = wrappedMainBranchCommit
			matchingCommits = append(matchingCommits, commitMatch)
			delete(unmatchedPrCommits, wrappedMainBranchCommit.hash())
		}
	}

	// verify unmatchedPrCommits is empty
	if len(unmatchedPrCommits) != 0 {
		return nil, fmt.Errorf("commits could be matched", unmatchedPrCommits)
	}

	return matchingCommits, nil
}

func collectCommitsFromPRs(prs []*github.PullRequest, client *github.Client, ctx context.Context) (map[uint32]commitMatch, error) {
	hashToCommitMatch := make(map[uint32]commitMatch)
	for _, pr := range prs {
		commits, _, err := client.PullRequests.ListCommits(ctx, owner, repo, *pr.Number, nil)
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

func createGitHubClient(token string, ctx context.Context) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	return client
}

func getMergedPullRequests(pullRequests string, client *github.Client, ctx context.Context) ([]*github.PullRequest, error) {
	var prs []*github.PullRequest
	for _, prNum := range strings.Split(strings.ReplaceAll(pullRequests, "#", ""), ",") {
		prInt, _ := strconv.Atoi(prNum)
		pr, _, err := client.PullRequests.Get(ctx, owner, repo, prInt)
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

func sortPRsByMergeDateAsc(prs []*github.PullRequest) {
	sort.Slice(prs, func(i, j int) bool {
		return prs[i].MergedAt.After(*(prs[j].MergedAt))
	})
}

func getOldestPRCreationDate(prs []*github.PullRequest) time.Time {
	oldest := time.Now()
	for _, pr := range prs {
		prCreationDate := *(pr.CreatedAt)
		if oldest.After(prCreationDate) { // TODO check if it works for different time zones, too
			oldest = prCreationDate
		}
	}
	return oldest
}

func getGitHubToken() string {
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		return token
	}

	// retrieve GITHUB_TOKEN using "gh config" command
	cmd := exec.Command("gh", "config", "get", "oauth_token", "-h", "github.com")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error retrieving GITHUB_TOKEN:", err)
		os.Exit(1)
	}

	token = strings.TrimSuffix(string(output), "\n")

	return token
}
