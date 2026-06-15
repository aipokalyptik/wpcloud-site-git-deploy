package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	version             = "0.1.0-go"
	defaultKeepReleases = 3
)

var (
	validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	validID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

type app struct {
	stateDir       string
	deploymentsDir string
	reposDir       string
	tmpDir         string
	binDir         string
}

type config struct {
	RepoURL      string
	Docroot      string
	DeploymentID string
	DefaultRef   string
	KeepReleases int
}

type deployState struct {
	docroot      string
	deploymentID string
	base         string
}

func main() {
	a, err := newApp()
	if err == nil {
		err = a.run(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wpcloud-site-git-deploy: %v\n", err)
		os.Exit(64)
	}
}

func newApp() (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state := os.Getenv("WPCLOUD_SITE_GIT_DEPLOY_HOME")
	if state == "" {
		state = filepath.Join(home, ".wpcloud-site-git-deploy")
	}
	return &app{
		stateDir:       state,
		deploymentsDir: filepath.Join(state, "deployments"),
		reposDir:       filepath.Join(state, "repos"),
		tmpDir:         filepath.Join(state, "tmp"),
		binDir:         filepath.Join(state, "bin"),
	}, nil
}

func (a *app) run(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	cmd := args[0]
	args = args[1:]
	switch cmd {
	case "init":
		return a.cmdInit(args)
	case "deploy":
		return a.cmdDeploy(args)
	case "update":
		return a.cmdUpdate(args)
	case "rollback":
		return a.cmdRollback(args)
	case "releases":
		return a.cmdReleases(args)
	case "branches":
		return a.cmdBranches(args)
	case "tags":
		return a.cmdTags(args)
	case "commits":
		return a.cmdCommits(args)
	case "status":
		return a.cmdStatus(args)
	case "__remote-deploy":
		return remoteDeploy(args)
	case "__exchange-rename":
		if len(args) != 2 {
			return errors.New("usage: exchange-rename OLD_PATH NEW_PATH")
		}
		return exchangePaths(args[0], args[1])
	case "--help", "-h":
		usage(os.Stdout)
		return nil
	case "--version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  wpcloud-site-git-deploy init <name> --repo URL --docroot /srv/htdocs --deployment-id ID --default-ref main
  wpcloud-site-git-deploy deploy <name> --branch BRANCH
  wpcloud-site-git-deploy deploy <name> --tag TAG
  wpcloud-site-git-deploy deploy <name> --commit SHA
  wpcloud-site-git-deploy update <name>
  wpcloud-site-git-deploy rollback <name> [--to RELEASE_ID]
  wpcloud-site-git-deploy releases <name>
  wpcloud-site-git-deploy branches <name> [--fetch] [--limit N]
  wpcloud-site-git-deploy tags <name> [--fetch] [--limit N]
  wpcloud-site-git-deploy commits <name> [--fetch] [--limit N]
  wpcloud-site-git-deploy status <name>
`)
}

func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runStdoutToStderr(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func output(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return strings.TrimRight(out.String(), "\n"), err
}

func requireName(name string) error {
	if !validName.MatchString(name) {
		return errors.New("deployment name must contain only letters, numbers, dot, underscore, or dash")
	}
	return nil
}

func requireID(field, id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("%s must be a normalized id", field)
	}
	return nil
}

func (a *app) ensureDirs() error {
	for _, dir := range []string{a.deploymentsDir, a.reposDir, a.tmpDir, a.binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) configPath(name string) string {
	return filepath.Join(a.deploymentsDir, name+".env")
}

func (a *app) loadConfig(name string) (config, error) {
	var cfg config
	if err := requireName(name); err != nil {
		return cfg, err
	}
	path := a.configPath(name)
	values, err := readEnvFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, fmt.Errorf("deployment is not initialized: %s", name)
		}
		return cfg, err
	}
	cfg.RepoURL = values["repo_url"]
	cfg.Docroot = values["docroot"]
	cfg.DeploymentID = values["deployment_id"]
	cfg.DefaultRef = values["default_ref"]
	cfg.KeepReleases = defaultKeepReleases
	if v := values["keep_releases"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, errors.New("keep-releases must be a positive integer")
		}
		cfg.KeepReleases = n
	}
	if cfg.RepoURL == "" || cfg.Docroot == "" || cfg.DeploymentID == "" || cfg.DefaultRef == "" {
		return cfg, fmt.Errorf("deployment config is incomplete: %s", name)
	}
	return cfg, nil
}

func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if unq, err := strconv.Unquote(val); err == nil {
			val = unq
		} else if len(val) >= 2 && strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") {
			val = strings.ReplaceAll(val[1:len(val)-1], `'\''`, `'`)
		}
		out[key] = val
	}
	return out, sc.Err()
}

func writeEnvFile(path string, values map[string]string) error {
	var b strings.Builder
	keys := []string{"repo_url", "docroot", "deployment_id", "default_ref", "keep_releases", "release_id", "ref_mode", "ref_value", "commit", "deployed_at"}
	for _, key := range keys {
		if val, ok := values[key]; ok {
			fmt.Fprintf(&b, "%s=%s\n", key, strconv.Quote(val))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func (a *app) repoDir(name string) string {
	return filepath.Join(a.reposDir, name)
}

func (a *app) ensureRepoCache(name string, cfg config) error {
	repoDir := a.repoDir(name)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		return run(repoDir, "git", "remote", "set-url", "origin", cfg.RepoURL)
	}
	if err := os.RemoveAll(repoDir); err != nil {
		return err
	}
	return run("", "git", "clone", cfg.RepoURL, repoDir)
}

func (a *app) fetchRepo(name string, cfg config) error {
	repoDir := a.repoDir(name)
	if err := a.ensureRepoCache(name, cfg); err != nil {
		return err
	}
	if err := run(repoDir, "git", "fetch", "--tags", "--prune", "origin"); err != nil {
		return err
	}
	return run("", "git", "-C", repoDir, "gc", "--auto")
}

func (a *app) cmdInit(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	name := args[0]
	if err := requireName(name); err != nil {
		return err
	}
	var repo, docroot, deploymentID, defaultRef string
	keep := defaultKeepReleases
	for i := 1; i < len(args); {
		switch args[i] {
		case "--repo":
			repo, i = needValue(args, i)
		case "--docroot":
			docroot, i = needValue(args, i)
		case "--deployment-id":
			deploymentID, i = needValue(args, i)
		case "--default-ref":
			defaultRef, i = needValue(args, i)
		case "--keep-releases":
			v, next := needValue(args, i)
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return errors.New("--keep-releases must be a positive integer")
			}
			keep, i = n, next
		default:
			return fmt.Errorf("unknown init argument: %s", args[i])
		}
	}
	if repo == "" {
		return errors.New("--repo is required")
	}
	if docroot == "" {
		return errors.New("--docroot is required")
	}
	if deploymentID == "" {
		return errors.New("--deployment-id is required")
	}
	if defaultRef == "" {
		return errors.New("--default-ref is required")
	}
	if err := requireID("deployment-id", deploymentID); err != nil {
		return err
	}
	if err := a.ensureDirs(); err != nil {
		return err
	}
	path := a.configPath(name)
	if err := writeEnvFile(path, map[string]string{
		"repo_url":      repo,
		"docroot":       docroot,
		"deployment_id": deploymentID,
		"default_ref":   defaultRef,
		"keep_releases": strconv.Itoa(keep),
	}); err != nil {
		return err
	}
	fmt.Printf("initialized %s\n", name)
	return nil
}

func needValue(args []string, i int) (string, int) {
	if i+1 >= len(args) {
		return "", len(args)
	}
	return args[i+1], i + 2
}

func (a *app) cmdDeploy(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	name := args[0]
	var mode, value string
	for i := 1; i < len(args); {
		switch args[i] {
		case "--branch":
			if mode != "" {
				return errors.New("choose only one ref")
			}
			mode, value, i = "branch", args[i+1], i+2
		case "--tag":
			if mode != "" {
				return errors.New("choose only one ref")
			}
			mode, value, i = "tag", args[i+1], i+2
		case "--commit":
			if mode != "" {
				return errors.New("choose only one ref")
			}
			mode, value, i = "commit", args[i+1], i+2
		default:
			return fmt.Errorf("unknown deploy argument: %s", args[i])
		}
	}
	if mode == "" || value == "" {
		return errors.New("deploy requires --branch, --tag, or --commit")
	}
	return a.deployRef(name, mode, value)
}

func (a *app) cmdUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	cfg, err := a.loadConfig(args[0])
	if err != nil {
		return err
	}
	return a.deployRef(args[0], "branch", cfg.DefaultRef)
}

func (a *app) deployRef(name, mode, value string) error {
	cfg, err := a.loadConfig(name)
	if err != nil {
		return err
	}
	if err := a.ensureDirs(); err != nil {
		return err
	}
	if err := a.fetchRepo(name, cfg); err != nil {
		return err
	}
	commit, err := a.resolveRef(name, mode, value)
	if err != nil {
		return err
	}
	releaseID := makeReleaseID(commit)
	worktree, err := a.createWorktree(name, cfg, commit, releaseID)
	if err != nil {
		return err
	}
	defer a.cleanupWorktree(name, worktree, releaseID)
	if err := a.copyWorktreeToIncoming(cfg, worktree, releaseID); err != nil {
		return err
	}
	if err := promoteRelease(cfg.Docroot, cfg.DeploymentID, releaseID, cfg.KeepReleases, ""); err != nil {
		return err
	}
	if err := a.writeReleaseMetadata(cfg, releaseID, mode, value, commit); err != nil {
		return err
	}
	fmt.Printf("%s %s %s\n", releaseID, mode, commit)
	return nil
}

func (a *app) resolveRef(name, mode, value string) (string, error) {
	repoDir := a.repoDir(name)
	switch mode {
	case "branch":
		return output(repoDir, "git", "rev-parse", "refs/remotes/origin/"+value+"^{commit}")
	case "tag":
		return output(repoDir, "git", "rev-parse", "refs/tags/"+value+"^{commit}")
	case "commit":
		return output(repoDir, "git", "rev-parse", value+"^{commit}")
	default:
		return "", fmt.Errorf("unknown ref mode: %s", mode)
	}
}

func makeReleaseID(commit string) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s-%s", time.Now().UTC().Format("20060102150405"), commit[:12], hex.EncodeToString(b[:]))
}

func (a *app) createWorktree(name string, cfg config, commit, releaseID string) (string, error) {
	repoDir := a.repoDir(name)
	worktree := filepath.Join(a.tmpDir, name, releaseID, "source")
	_ = os.RemoveAll(filepath.Join(a.tmpDir, name, releaseID))
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return "", err
	}
	_ = run(repoDir, "git", "worktree", "prune")
	if err := runStdoutToStderr(repoDir, "git", "worktree", "add", "--detach", worktree, commit); err != nil {
		return "", err
	}
	if err := prepareGitFeatures(worktree); err != nil {
		a.cleanupWorktree(name, worktree, releaseID)
		return "", err
	}
	return worktree, nil
}

func (a *app) cleanupWorktree(name, worktree, releaseID string) {
	repoDir := a.repoDir(name)
	if err := run(repoDir, "git", "worktree", "remove", "--force", worktree); err != nil {
		_ = os.RemoveAll(worktree)
	}
	_ = run(repoDir, "git", "worktree", "prune")
	_ = os.RemoveAll(filepath.Join(a.tmpDir, name, releaseID))
	_ = os.Remove(filepath.Join(a.tmpDir, name))
}

func prepareGitFeatures(worktree string) error {
	if _, err := os.Stat(filepath.Join(worktree, ".gitmodules")); err == nil {
		if err := run(worktree, "git", "submodule", "update", "--init", "--recursive"); err != nil {
			return err
		}
		status, err := output(worktree, "git", "submodule", "status", "--recursive")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "-") {
				return errors.New("one or more submodules are uninitialized")
			}
		}
	}
	filesOut, err := output(worktree, "git", "ls-files", "-z")
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", worktree, "check-attr", "filter", "--stdin", "-z")
	cmd.Stdin = strings.NewReader(filesOut)
	attrOut, err := cmd.Output()
	if err != nil {
		return err
	}
	var lfsPaths []string
	parts := bytes.Split(attrOut, []byte{0})
	for i := 0; i+2 < len(parts); i += 3 {
		if string(parts[i+2]) == "lfs" {
			lfsPaths = append(lfsPaths, string(parts[i]))
		}
	}
	if len(lfsPaths) == 0 {
		return nil
	}
	if _, err := exec.LookPath("git-lfs"); err != nil {
		return errors.New("git-lfs is required for repositories using Git LFS")
	}
	if err := run(worktree, "git", "lfs", "install", "--local"); err != nil {
		return err
	}
	if err := run(worktree, "git", "lfs", "pull"); err != nil {
		return err
	}
	for _, p := range lfsPaths {
		data, err := os.ReadFile(filepath.Join(worktree, p))
		if err == nil && bytes.HasPrefix(data, []byte("version https://git-lfs.github.com/spec/v1\n")) {
			return errors.New("Git LFS pointer files remain after git lfs pull")
		}
	}
	return nil
}

func defaultExcludeFile(path string) error {
	return os.WriteFile(path, []byte(strings.Join([]string{
		".git", ".git/", ".gitignore", ".gitattributes", ".gitmodules", ".github/",
		".svn/", ".hg/", ".bzr/", ".aws/", ".ssh/", ".env", ".env.*", ".npmrc", ".pypirc", ".netrc", ".DS_Store", "",
	}, "\n")), 0o644)
}

func (a *app) copyWorktreeToIncoming(cfg config, worktree, releaseID string) error {
	base := filepath.Join(cfg.Docroot, ".github-ssh-deploy", "deployments", cfg.DeploymentID)
	incoming := filepath.Join(base, "incoming", releaseID)
	if err := os.RemoveAll(incoming); err != nil {
		return err
	}
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return err
	}
	excludeFile := filepath.Join(a.tmpDir, fmt.Sprintf("excludes.%d.txt", os.Getpid()))
	if err := defaultExcludeFile(excludeFile); err != nil {
		return err
	}
	defer os.Remove(excludeFile)
	args := []string{"-a", "--delete", "--exclude-from=" + excludeFile}
	if cur, err := currentReleaseID(base); err == nil && cur != "" {
		prev := filepath.Join(base, "releases", cur)
		if st, err := os.Stat(prev); err == nil && st.IsDir() {
			args = append(args, "--checksum", "--link-dest="+prev)
		}
	}
	args = append(args, worktree+string(os.PathSeparator), incoming+string(os.PathSeparator))
	return run("", "rsync", args...)
}

func (a *app) writeReleaseMetadata(cfg config, releaseID, mode, refValue, commit string) error {
	dir := filepath.Join(cfg.Docroot, ".github-ssh-deploy", "deployments", cfg.DeploymentID, "metadata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeEnvFile(filepath.Join(dir, releaseID+".env"), map[string]string{
		"release_id":  releaseID,
		"ref_mode":    mode,
		"ref_value":   refValue,
		"commit":      commit,
		"deployed_at": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (a *app) cmdRollback(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	name := args[0]
	var to string
	for i := 1; i < len(args); {
		switch args[i] {
		case "--to":
			to, i = needValue(args, i)
		default:
			return fmt.Errorf("unknown rollback argument: %s", args[i])
		}
	}
	cfg, err := a.loadConfig(name)
	if err != nil {
		return err
	}
	base := filepath.Join(cfg.Docroot, ".github-ssh-deploy", "deployments", cfg.DeploymentID)
	if to == "" {
		cur, _ := currentReleaseID(base)
		to, err = selectRollbackRelease(base, cur)
		if err != nil {
			return err
		}
	}
	if to == "" {
		return errors.New("no rollback release available")
	}
	if err := rollbackRelease(cfg.Docroot, cfg.DeploymentID, to); err != nil {
		return err
	}
	fmt.Printf("rolled back to %s\n", to)
	return nil
}

func (a *app) cmdReleases(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	cfg, err := a.loadConfig(args[0])
	if err != nil {
		return err
	}
	base := filepath.Join(cfg.Docroot, ".github-ssh-deploy", "deployments", cfg.DeploymentID)
	cur, _ := currentReleaseID(base)
	releasesDir := filepath.Join(base, "releases")
	entries, _ := os.ReadDir(releasesDir)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		fmt.Print(name)
		if name == cur {
			fmt.Print(" current")
		}
		meta, err := readEnvFile(filepath.Join(base, "metadata", name+".env"))
		if err == nil {
			fmt.Printf(" %s %s:%s", valueOr(meta["commit"], "unknown"), valueOr(meta["ref_mode"], "unknown"), valueOr(meta["ref_value"], "unknown"))
		}
		fmt.Println()
	}
	return nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func (a *app) cmdBranches(args []string) error {
	return a.inspectRefs(args, "branches")
}
func (a *app) cmdTags(args []string) error {
	return a.inspectRefs(args, "tags")
}
func (a *app) cmdCommits(args []string) error {
	return a.inspectRefs(args, "commits")
}

func (a *app) inspectRefs(args []string, kind string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	name := args[0]
	limit := 20
	fetch := false
	for i := 1; i < len(args); {
		switch args[i] {
		case "--fetch":
			fetch = true
			i++
		case "--limit":
			v, next := needValue(args, i)
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			limit, i = n, next
		default:
			return fmt.Errorf("unknown %s argument: %s", kind, args[i])
		}
	}
	cfg, err := a.loadConfig(name)
	if err != nil {
		return err
	}
	if fetch {
		err = a.fetchRepo(name, cfg)
	} else {
		err = a.ensureRepoCache(name, cfg)
	}
	if err != nil {
		return err
	}
	repoDir := a.repoDir(name)
	switch kind {
	case "branches":
		out, err := output(repoDir, "git", "for-each-ref", "refs/remotes/origin", "--format=%(refname:short)", "--sort=-committerdate")
		if err != nil {
			return err
		}
		printLimited(filterHead(stripOrigin(strings.Split(out, "\n"))), limit)
	case "tags":
		out, err := output(repoDir, "git", "for-each-ref", "refs/tags", "--format=%(refname:short)", "--sort=-creatordate")
		if err != nil {
			return err
		}
		printLimited(nonEmpty(strings.Split(out, "\n")), limit)
	case "commits":
		out, err := output(repoDir, "git", "log", "--format=%H %s", "-n", strconv.Itoa(limit), "origin/"+cfg.DefaultRef)
		if err != nil {
			return err
		}
		if out != "" {
			fmt.Println(out)
		}
	}
	return nil
}

func printLimited(lines []string, limit int) {
	for i, line := range lines {
		if i >= limit {
			break
		}
		fmt.Println(line)
	}
}

func stripOrigin(lines []string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, strings.TrimPrefix(l, "origin/"))
	}
	return out
}
func filterHead(lines []string) []string {
	var out []string
	for _, l := range nonEmpty(lines) {
		if l != "HEAD" {
			out = append(out, l)
		}
	}
	return out
}
func nonEmpty(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (a *app) cmdStatus(args []string) error {
	if len(args) == 0 {
		return errors.New("deployment name is required")
	}
	cfg, err := a.loadConfig(args[0])
	if err != nil {
		return err
	}
	base := filepath.Join(cfg.Docroot, ".github-ssh-deploy", "deployments", cfg.DeploymentID)
	cur, _ := currentReleaseID(base)
	fmt.Printf("name=%s\nrepo=%s\ndocroot=%s\ndeployment_id=%s\ndefault_ref=%s\ncurrent=%s\n", args[0], cfg.RepoURL, cfg.Docroot, cfg.DeploymentID, cfg.DefaultRef, cur)
	return nil
}

func currentReleaseID(base string) (string, error) {
	target, err := os.Readlink(filepath.Join(base, "current"))
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(target, "releases/"), nil
}

func remoteDeploy(args []string) error {
	var docroot, deploymentID, releaseID, rollbackTo, postDeploy string
	var keep int
	printClaims := false
	assertPublic := false
	for i := 0; i < len(args); {
		switch args[i] {
		case "--docroot":
			docroot, i = needValue(args, i)
		case "--deployment-id":
			deploymentID, i = needValue(args, i)
		case "--release-id":
			releaseID, i = needValue(args, i)
		case "--rollback-to":
			rollbackTo, i = needValue(args, i)
		case "--keep-releases":
			v, next := needValue(args, i)
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return errors.New("keep-releases must be a positive integer")
			}
			keep, i = n, next
		case "--post-deploy-file":
			postDeploy, i = needValue(args, i)
		case "--exchange-helper":
			_, i = needValue(args, i)
		case "--print-claims":
			printClaims = true
			i++
		case "--assert-public-symlinks":
			assertPublic = true
			i++
		default:
			return fmt.Errorf("unknown argument: %s", args[i])
		}
	}
	if docroot == "" {
		return errors.New("docroot is required")
	}
	if assertPublic {
		return assertPublicSymlinksUnderDocroot(docroot, "")
	}
	if err := requireID("deployment-id", deploymentID); err != nil {
		return err
	}
	if rollbackTo != "" {
		if releaseID != "" {
			return errors.New("--release-id cannot be used with --rollback-to")
		}
		if postDeploy != "" {
			return errors.New("--post-deploy-file cannot be used with --rollback-to")
		}
		if printClaims {
			return errors.New("--print-claims cannot be used with --rollback-to")
		}
		return rollbackRelease(docroot, deploymentID, rollbackTo)
	}
	if err := requireID("release-id", releaseID); err != nil {
		return err
	}
	if printClaims {
		return printReleaseClaims(docroot, deploymentID, releaseID)
	}
	if keep < 1 {
		return errors.New("keep-releases must be a positive integer")
	}
	return promoteRelease(docroot, deploymentID, releaseID, keep, postDeploy)
}

func promoteRelease(docroot, deploymentID, releaseID string, keep int, postDeploy string) error {
	return promoteReleaseWithOptions(docroot, deploymentID, releaseID, keep, postDeploy, false)
}

func printReleaseClaims(docroot, deploymentID, releaseID string) error {
	return promoteReleaseWithOptions(docroot, deploymentID, releaseID, 1, "", true)
}

func promoteReleaseWithOptions(docroot, deploymentID, releaseID string, keep int, postDeploy string, printClaims bool) error {
	base := filepath.Join(docroot, ".github-ssh-deploy", "deployments", deploymentID)
	incoming := filepath.Join(base, "incoming", releaseID)
	releaseDir := filepath.Join(base, "releases", releaseID)
	if err := os.MkdirAll(filepath.Join(base, "incoming"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, "releases"), 0o755); err != nil {
		return err
	}
	unlock, err := acquireLock(filepath.Join(base, "deploy.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if st, err := os.Stat(incoming); err != nil || !st.IsDir() {
		return fmt.Errorf("incoming release does not exist: %s", incoming)
	}
	exchanged := filepath.Join(base, "exchanged_paths")
	_ = cleanupExchangedPaths(exchanged)
	_ = os.Remove(exchanged)
	sweepStaleScratchDirs(base)
	scratch, err := os.MkdirTemp(base, ".tmp.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	boundaries, err := discoverBoundaryClaims(docroot)
	if err != nil {
		return err
	}
	if printClaims {
		claims, err := computeClaims(incoming, boundaries)
		if err != nil {
			return err
		}
		for _, claim := range claims {
			fmt.Println(claim)
		}
		return nil
	}
	if _, err := os.Stat(releaseDir); err == nil {
		return fmt.Errorf("release already exists: %s", releaseDir)
	}
	pt, err := prepareClaimTransition(docroot, deploymentID, base, incoming, scratch, boundaries)
	if err != nil {
		return err
	}
	if err := os.Rename(incoming, releaseDir); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(releaseDir, now, now)
	if err := applyClaimTransition(docroot, deploymentID, base, releaseID, pt); err != nil {
		return err
	}
	if postDeploy != "" {
		if _, err := os.Stat(postDeploy); err != nil {
			return fmt.Errorf("post-deploy file does not exist: %s", postDeploy)
		}
		if err := run(docroot, "bash", "-e", postDeploy); err != nil {
			return err
		}
	}
	if err := pruneReleases(filepath.Join(base, "releases"), keep, releaseID); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "remote-deploy.sh: current=releases/%s\n", releaseID)
	return nil
}

type transition struct {
	newClaims     []string
	removedClaims []string
}

func prepareClaimTransition(docroot, deploymentID, base, targetReleaseDir, scratch string, boundaries []string) (transition, error) {
	protected, err := discoverProtectedAnchors(docroot)
	if err != nil {
		return transition{}, err
	}
	var oldReleaseClaims []string
	if target, err := os.Readlink(filepath.Join(base, "current")); err == nil {
		oldReleaseClaims, _ = computeClaims(filepath.Join(base, target), boundaries)
	}
	materialized, err := discoverMaterializedPublicClaims(docroot, deploymentID)
	if err != nil {
		return transition{}, err
	}
	oldClaims := unique(append(oldReleaseClaims, materialized...))
	newClaims, err := computeClaims(targetReleaseDir, boundaries)
	if err != nil {
		return transition{}, err
	}
	if err := validateClaimsNotProtected(newClaims, protected); err != nil {
		return transition{}, err
	}
	return transition{newClaims: newClaims, removedClaims: difference(oldClaims, newClaims)}, nil
}

func applyClaimTransition(docroot, deploymentID, base, releaseID string, tr transition) error {
	exchanged := filepath.Join(base, "exchanged_paths")
	if err := os.WriteFile(exchanged, nil, 0o644); err != nil {
		return err
	}
	cleanupOverlappingRemovedClaims(docroot, deploymentID, tr.removedClaims, tr.newClaims)
	if err := reconcileNewClaims(docroot, deploymentID, tr.newClaims, exchanged); err != nil {
		return err
	}
	if err := switchCurrent(base, releaseID); err != nil {
		return err
	}
	if target, _ := os.Readlink(filepath.Join(base, "current")); target != "releases/"+releaseID {
		return fmt.Errorf("current does not point to releases/%s", releaseID)
	}
	_ = cleanupExchangedPaths(exchanged)
	_ = os.Remove(exchanged)
	cleanupRemovedClaims(docroot, deploymentID, tr.removedClaims)
	return assertClaimSymlinksUnderDocroot(docroot, tr.newClaims)
}

func rollbackRelease(docroot, deploymentID, rollbackTo string) error {
	base := filepath.Join(docroot, ".github-ssh-deploy", "deployments", deploymentID)
	releaseDir := filepath.Join(base, "releases", rollbackTo)
	if err := os.MkdirAll(filepath.Join(base, "releases"), 0o755); err != nil {
		return err
	}
	unlock, err := acquireLock(filepath.Join(base, "deploy.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if st, err := os.Stat(releaseDir); err != nil || !st.IsDir() {
		return fmt.Errorf("rollback release does not exist: %s", releaseDir)
	}
	exchanged := filepath.Join(base, "exchanged_paths")
	_ = cleanupExchangedPaths(exchanged)
	_ = os.Remove(exchanged)
	sweepStaleScratchDirs(base)
	scratch, err := os.MkdirTemp(base, ".tmp.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	boundaries, err := discoverBoundaryClaims(docroot)
	if err != nil {
		return err
	}
	tr, err := prepareClaimTransition(docroot, deploymentID, base, releaseDir, scratch, boundaries)
	if err != nil {
		return err
	}
	if err := applyClaimTransition(docroot, deploymentID, base, rollbackTo, tr); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "remote-deploy.sh: current=releases/%s\n", rollbackTo)
	return nil
}

func acquireLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func sweepStaleScratchDirs(base string) {
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".tmp.") {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}

func switchCurrent(base, releaseID string) error {
	tmp := filepath.Join(base, ".current."+releaseID+fmt.Sprintf(".%d", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink("releases/"+releaseID, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(base, "current"))
}

func pruneReleases(releasesDir string, keep int, active string) error {
	entries, _ := os.ReadDir(releasesDir)
	type item struct {
		name string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, _ := e.Info()
		items = append(items, item{e.Name(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	retained := 0
	for _, it := range items {
		if it.name == active {
			continue
		}
		if retained < keep-1 {
			retained++
			continue
		}
		_ = os.RemoveAll(filepath.Join(releasesDir, it.name))
	}
	return nil
}

func publicSymlinkTarget(deploymentID, claim string) string {
	parent := claim
	prefix := ""
	if strings.Contains(parent, "/") {
		parent = parent[:strings.LastIndex(parent, "/")]
		for parent != "" {
			prefix = "../" + prefix
			if strings.Contains(parent, "/") {
				parent = parent[:strings.LastIndex(parent, "/")]
			} else {
				parent = ""
			}
		}
	}
	return prefix + ".github-ssh-deploy/deployments/" + deploymentID + "/current/" + claim
}

func reconcileNewClaims(docroot, deploymentID string, claims []string, exchangedFile string) error {
	for _, claim := range claims {
		publicPath := filepath.Join(docroot, filepath.FromSlash(claim))
		parent := filepath.Dir(publicPath)
		target := publicSymlinkTarget(deploymentID, claim)
		tmpLink := filepath.Join(parent, "."+filepath.Base(publicPath)+fmt.Sprintf(".github-ssh-deploy.%d", os.Getpid()))
		if err := rejectForeignDeploymentAncestorClaim(docroot, deploymentID, claim); err != nil {
			return err
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		if err := rejectForeignDeploymentClaim(deploymentID, claim, publicPath); err != nil {
			return err
		}
		if err := rejectForeignDeploymentDescendantClaim(deploymentID, claim, publicPath); err != nil {
			return err
		}
		_ = os.Remove(tmpLink)
		if corrupt := os.Getenv("WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET"); corrupt != "" {
			target = corrupt
		}
		if err := os.Symlink(target, tmpLink); err != nil {
			return err
		}
		if _, err := os.Lstat(publicPath); os.IsNotExist(err) {
			if err := os.Rename(tmpLink, publicPath); err != nil {
				return err
			}
			continue
		}
		if err := exchangePaths(tmpLink, publicPath); err != nil {
			_ = os.Remove(tmpLink)
			return fmt.Errorf("exchange helper failed to reclaim path: %s", claim)
		}
		f, err := os.OpenFile(exchangedFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(f, tmpLink)
		_ = f.Close()
	}
	return nil
}

func cleanupExchangedPaths(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p != "" {
			_ = os.RemoveAll(p)
		}
	}
	return sc.Err()
}

func deploymentOwnerFromTarget(target string) (string, bool) {
	parts := strings.Split(target, "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == ".github-ssh-deploy" && parts[i+1] == "deployments" && parts[i+3] == "current" {
			return parts[i+2], true
		}
	}
	return "", false
}

func rejectForeignDeploymentClaim(deploymentID, claim, publicPath string) error {
	if fi, err := os.Lstat(publicPath); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, _ := os.Readlink(publicPath)
	if owner, ok := deploymentOwnerFromTarget(target); ok && owner != deploymentID {
		return fmt.Errorf("claim owned by another deployment: %s", claim)
	}
	return nil
}

func rejectForeignDeploymentAncestorClaim(docroot, deploymentID, claim string) error {
	ancestor := docroot
	remainder := claim
	for strings.Contains(remainder, "/") {
		component, rest, _ := strings.Cut(remainder, "/")
		remainder = rest
		ancestor = filepath.Join(ancestor, component)
		if fi, err := os.Lstat(ancestor); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(ancestor)
			if owner, ok := deploymentOwnerFromTarget(target); ok && owner != deploymentID {
				return fmt.Errorf("claim owned by another deployment: %s", claim)
			}
		}
	}
	return nil
}

func rejectForeignDeploymentDescendantClaim(deploymentID, claim, publicPath string) error {
	fi, err := os.Lstat(publicPath)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return filepath.WalkDir(publicPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == publicPath {
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, _ := os.Readlink(path)
		if owner, ok := deploymentOwnerFromTarget(target); ok && owner != deploymentID {
			return fmt.Errorf("claim contains another deployment: %s", claim)
		}
		return nil
	})
}

func removeExactClaimSymlink(docroot, deploymentID, claim string) {
	publicPath := filepath.Join(docroot, filepath.FromSlash(claim))
	fi, err := os.Lstat(publicPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return
	}
	target, _ := os.Readlink(publicPath)
	if target == publicSymlinkTarget(deploymentID, claim) {
		_ = os.Remove(publicPath)
	}
}

func cleanupRemovedClaims(docroot, deploymentID string, claims []string) {
	for _, claim := range claims {
		removeExactClaimSymlink(docroot, deploymentID, claim)
	}
}

func claimsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func cleanupOverlappingRemovedClaims(docroot, deploymentID string, removed, new []string) {
	for _, r := range removed {
		for _, n := range new {
			if claimsOverlap(r, n) {
				removeExactClaimSymlink(docroot, deploymentID, r)
				break
			}
		}
	}
}

func discoverMaterializedPublicClaims(docroot, deploymentID string) ([]string, error) {
	// Preserve observable scan behavior for tests and for parity with the Bash audit path.
	cmd := exec.Command("find", docroot, "-path", filepath.Join(docroot, ".github-ssh-deploy"), "-prune", "-o", "-type", "l", "-print0")
	out, _ := cmd.Output()
	var claims []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		linkPath := string(raw)
		claim, err := filepath.Rel(docroot, linkPath)
		if err != nil {
			continue
		}
		claim = filepath.ToSlash(claim)
		target, _ := os.Readlink(linkPath)
		if target == publicSymlinkTarget(deploymentID, claim) {
			claims = append(claims, claim)
		}
	}
	return unique(claims), nil
}

func normalizePublicPath(v string) string {
	v = strings.TrimPrefix(v, "./")
	v = strings.TrimRight(v, "/")
	if v == "." {
		return ""
	}
	return v
}

func discoverBoundaryClaims(docroot string) ([]string, error) {
	if p := os.Getenv("GITHUB_SSH_DEPLOY_BOUNDARIES_FILE"); p != "" {
		return readPathOverride(p)
	}
	var out []string
	filepath.WalkDir(docroot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if ok && (st.Uid == 0 || st.Gid == 0) && info.Mode()&os.ModeSticky != 0 {
			rel, _ := filepath.Rel(docroot, path)
			out = append(out, normalizePublicPath(filepath.ToSlash(rel)))
		}
		return nil
	})
	return unique(out), nil
}

func discoverProtectedAnchors(docroot string) ([]string, error) {
	if p := os.Getenv("GITHUB_SSH_DEPLOY_PROTECTED_ANCHORS_FILE"); p != "" {
		return readPathOverride(p)
	}
	var out []string
	filepath.WalkDir(docroot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if ok && (st.Uid == 0 || st.Gid == 0) && !pathIsWritable(path) {
			rel, _ := filepath.Rel(docroot, path)
			out = append(out, normalizePublicPath(filepath.ToSlash(rel)))
		}
		return nil
	})
	return unique(out), nil
}

func pathIsWritable(path string) bool {
	const writeOK = 0x2
	return syscall.Access(path, writeOK) == nil
}

func readPathOverride(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = normalizePublicPath(strings.TrimSpace(line))
		if line != "" {
			out = append(out, line)
		}
	}
	return unique(out), nil
}

func computeClaims(releaseTree string, boundaries []string) ([]string, error) {
	if st, err := os.Stat(releaseTree); err != nil || !st.IsDir() {
		return nil, nil
	}
	var claims []string
	err := filepath.WalkDir(releaseTree, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == releaseTree {
			return nil
		}
		if d.IsDir() && d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		rel, _ := filepath.Rel(releaseTree, path)
		pub := filepath.ToSlash(rel)
		if pub == ".git" || strings.HasPrefix(pub, ".git/") || pub == ".github-ssh-deploy" || strings.HasPrefix(pub, ".github-ssh-deploy/") {
			return nil
		}
		claims = append(claims, claimForPath(pub, boundaries))
		return nil
	})
	return unique(claims), err
}

func claimForPath(publicPath string, boundaries []string) string {
	best := ""
	for _, b := range boundaries {
		if b != "" && strings.HasPrefix(publicPath, b+"/") && len(b) > len(best) {
			best = b
		}
	}
	if best != "" {
		rem := strings.TrimPrefix(publicPath, best+"/")
		next := rem
		if i := strings.Index(next, "/"); i >= 0 {
			next = next[:i]
		}
		return best + "/" + next
	}
	if i := strings.Index(publicPath, "/"); i >= 0 {
		return publicPath[:i]
	}
	return publicPath
}

func validateClaimsNotProtected(claims, protected []string) error {
	for _, c := range claims {
		for _, p := range protected {
			if c == p || strings.HasPrefix(c, p+"/") || strings.HasPrefix(p, c+"/") {
				return fmt.Errorf("protected path: %s", c)
			}
		}
	}
	return nil
}

func difference(old, new []string) []string {
	newSet := map[string]bool{}
	for _, n := range new {
		newSet[n] = true
	}
	var out []string
	for _, o := range old {
		if !newSet[o] {
			out = append(out, o)
		}
	}
	return out
}

func unique(in []string) []string {
	set := map[string]bool{}
	for _, v := range in {
		if v != "" {
			set[v] = true
		}
	}
	var out []string
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func assertPublicSymlinksUnderDocroot(docroot, deploymentFilter string) error {
	docrootReal, err := filepath.EvalSymlinks(docroot)
	if err != nil {
		return fmt.Errorf("docroot does not exist: %s", docroot)
	}
	home := os.Getenv("HOME")
	cmd := exec.Command("find", docroot, "-path", filepath.Join(docroot, ".github-ssh-deploy"), "-prune", "-o", "-type", "l", "-print0")
	out, _ := cmd.Output()
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		link := string(raw)
		target, _ := os.Readlink(link)
		if deploymentFilter != "" {
			if owner, ok := deploymentOwnerFromTarget(target); !ok || owner != deploymentFilter {
				continue
			}
		}
		rel, _ := filepath.Rel(docroot, link)
		rel = filepath.ToSlash(rel)
		if filepath.IsAbs(target) {
			return fmt.Errorf("public symlink target is absolute: %s", rel)
		}
		if home != "" && strings.Contains(target, home) {
			return fmt.Errorf("public symlink target contains HOME: %s", rel)
		}
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil || !strings.HasPrefix(resolved, docrootReal+string(os.PathSeparator)) {
			return fmt.Errorf("public symlink resolves outside docroot: %s", rel)
		}
	}
	return nil
}

func assertClaimSymlinksUnderDocroot(docroot string, claims []string) error {
	docrootReal, err := filepath.EvalSymlinks(docroot)
	if err != nil {
		return fmt.Errorf("docroot does not exist: %s", docroot)
	}
	home := os.Getenv("HOME")
	for _, claim := range claims {
		path := filepath.Join(docroot, filepath.FromSlash(claim))
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("public claim is not a symlink: %s", claim)
		}
		target, _ := os.Readlink(path)
		if filepath.IsAbs(target) {
			return fmt.Errorf("public symlink target is absolute: %s", claim)
		}
		if home != "" && strings.Contains(target, home) {
			return fmt.Errorf("public symlink target contains HOME: %s", claim)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.HasPrefix(resolved, docrootReal+string(os.PathSeparator)) {
			return fmt.Errorf("public symlink resolves outside docroot: %s", claim)
		}
	}
	return nil
}

func selectRollbackRelease(base, current string) (string, error) {
	metadataDir := filepath.Join(base, "metadata")
	var candidates []string
	if entries, err := os.ReadDir(metadataDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".env")
			if name == current {
				continue
			}
			if st, err := os.Stat(filepath.Join(base, "releases", name)); err == nil && st.IsDir() {
				candidates = append(candidates, name)
			}
		}
	}
	if len(candidates) == 0 {
		if entries, err := os.ReadDir(filepath.Join(base, "releases")); err == nil {
			for _, e := range entries {
				if e.IsDir() && e.Name() != current {
					candidates = append(candidates, e.Name())
				}
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		ai, _ := os.Stat(filepath.Join(base, "releases", candidates[i]))
		aj, _ := os.Stat(filepath.Join(base, "releases", candidates[j]))
		return ai.ModTime().After(aj.ModTime())
	})
	if len(candidates) == 0 {
		return "", nil
	}
	return candidates[0], nil
}

func exchangePaths(left, right string) error {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		lp, err := syscall.BytePtrFromString(left)
		if err != nil {
			return err
		}
		rp, err := syscall.BytePtrFromString(right)
		if err != nil {
			return err
		}
		_, _, errno := syscall.Syscall6(316, ^uintptr(99), uintptr(unsafe.Pointer(lp)), ^uintptr(99), uintptr(unsafe.Pointer(rp)), uintptr(0x2), 0)
		if errno == 0 {
			return nil
		}
	}
	tmp := fmt.Sprintf("%s.exchange.%d", left, os.Getpid())
	if err := os.Rename(left, tmp); err != nil {
		return err
	}
	if err := os.Rename(right, left); err != nil {
		return err
	}
	return os.Rename(tmp, right)
}
