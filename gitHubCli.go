package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// getGitHubToken returns value of GITHUB_TOKEN environment variable
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

	return strings.TrimSuffix(string(output), "\n")
}

type repoView struct {
	Name  string `json:"name"`
	Owner owner  `json:"owner"`
}

type owner struct {
	Id    string `json:"id"`
	Login string `json:"login"`
}

// getActiveRepoInfo returns Name and Owner of the active git repository
func getActiveRepoInfo() (*repoView, error) {
	cmd := exec.Command("gh", "repo", "view", "--json", "name,owner")
	ghRepoJson, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error executing 'gh repo view --json name,owner: %v", err)
	}

	var repo *repoView
	if err := json.Unmarshal(ghRepoJson, &repo); err != nil {
		return nil, fmt.Errorf("can not unmarshal JSON: %v", err)
	}

	return repo, nil
}
