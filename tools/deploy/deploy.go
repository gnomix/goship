package main

// This script polls ETCD and executes Chef knife solo cook.

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/coreos/go-etcd/etcd"
	"github.com/gengo/goship/lib"
	"github.com/google/go-github/github"
	"github.com/kylelemons/go-gypsy/yaml"
	"golang.org/x/oauth2"
)

var (
	deployProj = flag.String("p", "", "project (required)")
	deployEnv  = flag.String("e", "", "environment (required)")
	configFile = flag.String("g", "/tmp/deploy.yaml", "shared config setting ( default /tmp/deploy.yaml)")
	pullOnly   = flag.Bool("o", false, "chef update only (default false)")
	skipUpdate = flag.Bool("m", false, "skip the chef update (default false)")
	bootstrap  = flag.Bool("b", false, "bootstrap a server ( default false)")
)

// gitHubPaginationLimit is the default pagination limit for requests to the GitHub API that return multiple items.
const (
	gitHubPaginationLimit = 30
	gitHubAPITokenEnvVar  = "GITHUB_API_TOKEN"
)

// config contains the information from config.yml.
type config struct {
	chefRepo   string
	chefPath   string
	knifePath  string
	pemKey     string
	deployUser string
	etcdServer string
}

func checkMissingConf(s, v, f string) {
	if len(s) < 1 {
		log.Fatalf("Warning: Missing %s in config file [%s]", v, f)
	}
}

func parseConfig() (c config) {
	config, err := yaml.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Fatal: Can't parse conf file %s", *configFile)
	}
	c.chefRepo, err = config.Get("chef_repo")
	checkMissingConf(c.chefRepo, "chef_repo", *configFile)
	c.chefPath, err = config.Get("chef_path")
	checkMissingConf(c.chefPath, "chef_path", *configFile)
	c.knifePath, err = config.Get("knife_path")
	checkMissingConf(c.knifePath, "knife_path", *configFile)
	c.pemKey, err = config.Get("pem_key")
	checkMissingConf(c.pemKey, "pem_key", *configFile)
	c.deployUser, err = config.Get("deploy_user")
	checkMissingConf(c.deployUser, "deploy_user", *configFile)
	c.etcdServer, err = config.Get("etcd_server")
	if len(c.etcdServer) < 1 {
		c.etcdServer = "http://127.0.0.1:4001"
	}
	return c
}

// updateChefRepo ensures the lates chef cookbooks are pulled before deploying.
// Checks github first and ignores pull if already up to date.
func updateChefRepo(conf config) {
	githubToken := os.Getenv(gitHubAPITokenEnvVar)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	client := github.NewClient(oauth2.NewClient(oauth2.NoContext, ts))
	s := "git --git-dir=" + conf.chefRepo + "/.git rev-parse HEAD"
	localHash, _ := execCmd(s, conf)
	commits, _, err := client.Repositories.ListCommits("Gengo", "devops-tools", nil)
	if err != nil {
		log.Fatal("ERROR:  failed to get commits from GitHub: Please try again later ", err)
	}
	remoteHash := *commits[0].SHA
	if localHash == remoteHash {
		log.Printf("Local Chef is up to date: Skipping Sync")
	} else {
		log.Printf("Chef is not up to date: \n %s does not equal %s", localHash, remoteHash)
		log.Println("Updating devops-tools")
		os.Setenv("GIT_SSH", "/tmp/private_code/wrap-ssh4git.sh")
		gitcmd := "/usr/bin/git --git-dir=" + conf.chefRepo + "/.git --work-tree=" + conf.chefRepo + " pull origin master"
		s := gitcmd
		_, err := execCmd(s, conf)
		if err != nil {
			log.Fatal("ERROR:  Failed to pull latest devops_tools: ", err)
		}
		log.Println("Devops Tools Updated")
	}
}

func execCmd(icmd string, conf config) (output string, err error) {
	os.Chdir(conf.chefPath)

	parts := strings.Fields(icmd)
	head := parts[0]
	parts = parts[1:len(parts)]

	cmd := exec.Command(head, parts...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		o := scanner.Text()
		output += o
		fmt.Println(o)

	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading standard output stream: %s", err)
	}
	scanner = bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Println(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading standard error stream: %s", err)
	}
	if err := cmd.Wait(); err != nil {
		log.Fatalf("Error waiting for Chef to complete %s", err)
	}
	return output, err
}

func main() {
	flag.Parse()
	conf := parseConfig()
	if *skipUpdate == false {
		updateChefRepo(conf)
	}
	if *pullOnly == false {
		c, err := goship.ParseETCD(etcd.NewClient([]string{conf.etcdServer}))
		if err != nil {
			log.Fatalf("Error parsing ETCD: %s", err)
		}
		projectEnv, err := goship.EnvironmentFromName(c.Projects, *deployProj, *deployEnv)
		if err != nil {
			log.Fatalf("Error getting project %s %s %s", *deployProj, *deployEnv, err)
		}
		log.Printf("Deploying project name: %s environment Name: %s", *deployEnv, projectEnv.Name)
		servers := projectEnv.Hosts
		var d string
		for _, h := range servers {
			if *bootstrap == true {
				d = "knife solo bootstrap -c " + conf.knifePath + " -i " + conf.pemKey + " --no-host-key-verify " + conf.deployUser + "@" + h.URI
			} else {
				d = "knife solo cook -c " + conf.knifePath + " -i " + conf.pemKey + " --no-host-key-verify " + conf.deployUser + "@" + h.URI
			}
			log.Printf("Deploying to server: %s", h.URI)
			log.Printf("Preparing Knife command: %s", d)
			_, err := execCmd(d, conf)
			if err != nil {
				log.Fatalf("Error Executing command %s", err)
			}
		}
	}

}
