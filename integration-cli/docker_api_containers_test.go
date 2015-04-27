package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/vendor/src/code.google.com/p/go/src/pkg/archive/tar"
	"github.com/go-check/check"
)

func (s *DockerSuite) TestContainerApiGetAll(c *check.C) {
	startCount, err := getContainerCount()
	if err != nil {
		c.Fatalf("Cannot query container count: %v", err)
	}

	name := "getall"
	runCmd := exec.Command(dockerBinary, "run", "--name", name, "busybox", "true")
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		c.Fatalf("Error on container creation: %v, output: %q", err, out)
	}

	status, body, err := sockRequest("GET", "/containers/json?all=1", nil)
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	var inspectJSON []struct {
		Names []string
	}
	if err = json.Unmarshal(body, &inspectJSON); err != nil {
		c.Fatalf("unable to unmarshal response body: %v", err)
	}

	if len(inspectJSON) != startCount+1 {
		c.Fatalf("Expected %d container(s), %d found (started with: %d)", startCount+1, len(inspectJSON), startCount)
	}

	if actual := inspectJSON[0].Names[0]; actual != "/"+name {
		c.Fatalf("Container Name mismatch. Expected: %q, received: %q\n", "/"+name, actual)
	}
}

func (s *DockerSuite) TestContainerApiGetExport(c *check.C) {
	name := "exportcontainer"
	runCmd := exec.Command(dockerBinary, "run", "--name", name, "busybox", "touch", "/test")
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		c.Fatalf("Error on container creation: %v, output: %q", err, out)
	}

	status, body, err := sockRequest("GET", "/containers/"+name+"/export", nil)
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	found := false
	for tarReader := tar.NewReader(bytes.NewReader(body)); ; {
		h, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			c.Fatal(err)
		}
		if h.Name == "test" {
			found = true
			break
		}
	}

	if !found {
		c.Fatalf("The created test file has not been found in the exported image")
	}
}

func (s *DockerSuite) TestContainerApiGetChanges(c *check.C) {
	name := "changescontainer"
	runCmd := exec.Command(dockerBinary, "run", "--name", name, "busybox", "rm", "/etc/passwd")
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		c.Fatalf("Error on container creation: %v, output: %q", err, out)
	}

	status, body, err := sockRequest("GET", "/containers/"+name+"/changes", nil)
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	changes := []struct {
		Kind int
		Path string
	}{}
	if err = json.Unmarshal(body, &changes); err != nil {
		c.Fatalf("unable to unmarshal response body: %v", err)
	}

	// Check the changelog for removal of /etc/passwd
	success := false
	for _, elem := range changes {
		if elem.Path == "/etc/passwd" && elem.Kind == 2 {
			success = true
		}
	}
	if !success {
		c.Fatalf("/etc/passwd has been removed but is not present in the diff")
	}
}

func (s *DockerSuite) TestContainerApiStartVolumeBinds(c *check.C) {
	name := "testing"
	config := map[string]interface{}{
		"Image":   "busybox",
		"Volumes": map[string]struct{}{"/tmp": {}},
	}

	status, _, err := sockRequest("POST", "/containers/create?name="+name, config)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	bindPath := randomUnixTmpDirPath("test")
	config = map[string]interface{}{
		"Binds": []string{bindPath + ":/tmp"},
	}
	status, _, err = sockRequest("POST", "/containers/"+name+"/start", config)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	pth, err := inspectFieldMap(name, "Volumes", "/tmp")
	if err != nil {
		c.Fatal(err)
	}

	if pth != bindPath {
		c.Fatalf("expected volume host path to be %s, got %s", bindPath, pth)
	}
}

// Test for GH#10618
func (s *DockerSuite) TestContainerApiStartDupVolumeBinds(c *check.C) {
	name := "testdups"
	config := map[string]interface{}{
		"Image":   "busybox",
		"Volumes": map[string]struct{}{"/tmp": {}},
	}

	status, _, err := sockRequest("POST", "/containers/create?name="+name, config)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	bindPath1 := randomUnixTmpDirPath("test1")
	bindPath2 := randomUnixTmpDirPath("test2")

	config = map[string]interface{}{
		"Binds": []string{bindPath1 + ":/tmp", bindPath2 + ":/tmp"},
	}
	status, body, err := sockRequest("POST", "/containers/"+name+"/start", config)
	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(err, check.IsNil)

	if !strings.Contains(string(body), "Duplicate volume") {
		c.Fatalf("Expected failure due to duplicate bind mounts to same path, instead got: %q with error: %v", string(body), err)
	}
}

func (s *DockerSuite) TestContainerApiStartVolumesFrom(c *check.C) {
	volName := "voltst"
	volPath := "/tmp"

	if out, _, err := runCommandWithOutput(exec.Command(dockerBinary, "run", "-d", "--name", volName, "-v", volPath, "busybox")); err != nil {
		c.Fatal(out, err)
	}

	name := "testing"
	config := map[string]interface{}{
		"Image":   "busybox",
		"Volumes": map[string]struct{}{volPath: {}},
	}

	status, _, err := sockRequest("POST", "/containers/create?name="+name, config)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	config = map[string]interface{}{
		"VolumesFrom": []string{volName},
	}
	status, _, err = sockRequest("POST", "/containers/"+name+"/start", config)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	pth, err := inspectFieldMap(name, "Volumes", volPath)
	if err != nil {
		c.Fatal(err)
	}
	pth2, err := inspectFieldMap(volName, "Volumes", volPath)
	if err != nil {
		c.Fatal(err)
	}

	if pth != pth2 {
		c.Fatalf("expected volume host path to be %s, got %s", pth, pth2)
	}
}

// Ensure that volumes-from has priority over binds/anything else
// This is pretty much the same as TestRunApplyVolumesFromBeforeVolumes, except with passing the VolumesFrom and the bind on start
func (s *DockerSuite) TestVolumesFromHasPriority(c *check.C) {
	volName := "voltst2"
	volPath := "/tmp"

	if out, _, err := runCommandWithOutput(exec.Command(dockerBinary, "run", "-d", "--name", volName, "-v", volPath, "busybox")); err != nil {
		c.Fatal(out, err)
	}

	name := "testing"
	config := map[string]interface{}{
		"Image":   "busybox",
		"Volumes": map[string]struct{}{volPath: {}},
	}

	status, _, err := sockRequest("POST", "/containers/create?name="+name, config)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	bindPath := randomUnixTmpDirPath("test")
	config = map[string]interface{}{
		"VolumesFrom": []string{volName},
		"Binds":       []string{bindPath + ":/tmp"},
	}
	status, _, err = sockRequest("POST", "/containers/"+name+"/start", config)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	pth, err := inspectFieldMap(name, "Volumes", volPath)
	if err != nil {
		c.Fatal(err)
	}
	pth2, err := inspectFieldMap(volName, "Volumes", volPath)
	if err != nil {
		c.Fatal(err)
	}

	if pth != pth2 {
		c.Fatalf("expected volume host path to be %s, got %s", pth, pth2)
	}
}

func (s *DockerSuite) TestGetContainerStats(c *check.C) {
	var (
		name   = "statscontainer"
		runCmd = exec.Command(dockerBinary, "run", "-d", "--name", name, "busybox", "top")
	)
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		c.Fatalf("Error on container creation: %v, output: %q", err, out)
	}
	type b struct {
		body []byte
		err  error
	}
	bc := make(chan b, 1)
	go func() {
		status, body, err := sockRequest("GET", "/containers/"+name+"/stats", nil)
		c.Assert(status, check.Equals, http.StatusOK)
		c.Assert(err, check.IsNil)
		bc <- b{body, err}
	}()

	// allow some time to stream the stats from the container
	time.Sleep(4 * time.Second)
	if _, err := runCommand(exec.Command(dockerBinary, "rm", "-f", name)); err != nil {
		c.Fatal(err)
	}

	// collect the results from the stats stream or timeout and fail
	// if the stream was not disconnected.
	select {
	case <-time.After(2 * time.Second):
		c.Fatal("stream was not closed after container was removed")
	case sr := <-bc:
		if sr.err != nil {
			c.Fatal(sr.err)
		}

		dec := json.NewDecoder(bytes.NewBuffer(sr.body))
		var s *types.Stats
		// decode only one object from the stream
		if err := dec.Decode(&s); err != nil {
			c.Fatal(err)
		}
	}
}

func (s *DockerSuite) TestGetStoppedContainerStats(c *check.C) {
	var (
		name   = "statscontainer"
		runCmd = exec.Command(dockerBinary, "create", "--name", name, "busybox", "top")
	)
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		c.Fatalf("Error on container creation: %v, output: %q", err, out)
	}

	go func() {
		// We'll never get return for GET stats from sockRequest as of now,
		// just send request and see if panic or error would happen on daemon side.
		status, _, err := sockRequest("GET", "/containers/"+name+"/stats", nil)
		c.Assert(status, check.Equals, http.StatusOK)
		c.Assert(err, check.IsNil)
	}()

	// allow some time to send request and let daemon deal with it
	time.Sleep(1 * time.Second)
}

func (s *DockerSuite) TestBuildApiDockerfilePath(c *check.C) {
	// Test to make sure we stop people from trying to leave the
	// build context when specifying the path to the dockerfile
	buffer := new(bytes.Buffer)
	tw := tar.NewWriter(buffer)
	defer tw.Close()

	dockerfile := []byte("FROM busybox")
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(dockerfile)),
	}); err != nil {
		c.Fatalf("failed to write tar file header: %v", err)
	}
	if _, err := tw.Write(dockerfile); err != nil {
		c.Fatalf("failed to write tar file content: %v", err)
	}
	if err := tw.Close(); err != nil {
		c.Fatalf("failed to close tar archive: %v", err)
	}

	status, body, err := sockRequestRaw("POST", "/build?dockerfile=../Dockerfile", buffer, "application/x-tar")
	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(err, check.IsNil)

	out, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	if !strings.Contains(string(out), "must be within the build context") {
		c.Fatalf("Didn't complain about leaving build context: %s", out)
	}
}

func (s *DockerSuite) TestBuildApiDockerFileRemote(c *check.C) {
	server, err := fakeStorage(map[string]string{
		"testD": `FROM busybox
COPY * /tmp/
RUN find / -name ba*
RUN find /tmp/`,
	})
	if err != nil {
		c.Fatal(err)
	}
	defer server.Close()

	status, body, err := sockRequestRaw("POST", "/build?dockerfile=baz&remote="+server.URL()+"/testD", nil, "application/json")
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	buf, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	// Make sure Dockerfile exists.
	// Make sure 'baz' doesn't exist ANYWHERE despite being mentioned in the URL
	out := string(buf)
	if !strings.Contains(out, "/tmp/Dockerfile") ||
		strings.Contains(out, "baz") {
		c.Fatalf("Incorrect output: %s", out)
	}
}

func (s *DockerSuite) TestBuildApiLowerDockerfile(c *check.C) {
	git, err := fakeGIT("repo", map[string]string{
		"dockerfile": `FROM busybox
RUN echo from dockerfile`,
	}, false)
	if err != nil {
		c.Fatal(err)
	}
	defer git.Close()

	status, body, err := sockRequestRaw("POST", "/build?remote="+git.RepoURL, nil, "application/json")
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	buf, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	out := string(buf)
	if !strings.Contains(out, "from dockerfile") {
		c.Fatalf("Incorrect output: %s", out)
	}
}

func (s *DockerSuite) TestBuildApiBuildGitWithF(c *check.C) {
	git, err := fakeGIT("repo", map[string]string{
		"baz": `FROM busybox
RUN echo from baz`,
		"Dockerfile": `FROM busybox
RUN echo from Dockerfile`,
	}, false)
	if err != nil {
		c.Fatal(err)
	}
	defer git.Close()

	// Make sure it tries to 'dockerfile' query param value
	status, body, err := sockRequestRaw("POST", "/build?dockerfile=baz&remote="+git.RepoURL, nil, "application/json")
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	buf, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	out := string(buf)
	if !strings.Contains(out, "from baz") {
		c.Fatalf("Incorrect output: %s", out)
	}
}

func (s *DockerSuite) TestBuildApiDoubleDockerfile(c *check.C) {
	testRequires(c, UnixCli) // dockerfile overwrites Dockerfile on Windows
	git, err := fakeGIT("repo", map[string]string{
		"Dockerfile": `FROM busybox
RUN echo from Dockerfile`,
		"dockerfile": `FROM busybox
RUN echo from dockerfile`,
	}, false)
	if err != nil {
		c.Fatal(err)
	}
	defer git.Close()

	// Make sure it tries to 'dockerfile' query param value
	status, body, err := sockRequestRaw("POST", "/build?remote="+git.RepoURL, nil, "application/json")
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	buf, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	out := string(buf)
	if !strings.Contains(out, "from Dockerfile") {
		c.Fatalf("Incorrect output: %s", out)
	}
}

func (s *DockerSuite) TestBuildApiDockerfileSymlink(c *check.C) {
	// Test to make sure we stop people from trying to leave the
	// build context when specifying a symlink as the path to the dockerfile
	buffer := new(bytes.Buffer)
	tw := tar.NewWriter(buffer)
	defer tw.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name:     "Dockerfile",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}); err != nil {
		c.Fatalf("failed to write tar file header: %v", err)
	}
	if err := tw.Close(); err != nil {
		c.Fatalf("failed to close tar archive: %v", err)
	}

	status, body, err := sockRequestRaw("POST", "/build", buffer, "application/x-tar")
	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(err, check.IsNil)

	out, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}

	// The reason the error is "Cannot locate specified Dockerfile" is because
	// in the builder, the symlink is resolved within the context, therefore
	// Dockerfile -> /etc/passwd becomes etc/passwd from the context which is
	// a nonexistent file.
	if !strings.Contains(string(out), "Cannot locate specified Dockerfile: Dockerfile") {
		c.Fatalf("Didn't complain about leaving build context: %s", out)
	}
}

// #9981 - Allow a docker created volume (ie, one in /var/lib/docker/volumes) to be used to overwrite (via passing in Binds on api start) an existing volume
func (s *DockerSuite) TestPostContainerBindNormalVolume(c *check.C) {
	out, _, err := runCommandWithOutput(exec.Command(dockerBinary, "create", "-v", "/foo", "--name=one", "busybox"))
	if err != nil {
		c.Fatal(err, out)
	}

	fooDir, err := inspectFieldMap("one", "Volumes", "/foo")
	if err != nil {
		c.Fatal(err)
	}

	out, _, err = runCommandWithOutput(exec.Command(dockerBinary, "create", "-v", "/foo", "--name=two", "busybox"))
	if err != nil {
		c.Fatal(err, out)
	}

	bindSpec := map[string][]string{"Binds": {fooDir + ":/foo"}}
	status, _, err := sockRequest("POST", "/containers/two/start", bindSpec)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	fooDir2, err := inspectFieldMap("two", "Volumes", "/foo")
	if err != nil {
		c.Fatal(err)
	}

	if fooDir2 != fooDir {
		c.Fatalf("expected volume path to be %s, got: %s", fooDir, fooDir2)
	}
}

func (s *DockerSuite) TestContainerApiPause(c *check.C) {
	defer unpauseAllContainers()
	runCmd := exec.Command(dockerBinary, "run", "-d", "busybox", "sleep", "30")
	out, _, err := runCommandWithOutput(runCmd)

	if err != nil {
		c.Fatalf("failed to create a container: %s, %v", out, err)
	}
	ContainerID := strings.TrimSpace(out)

	status, _, err := sockRequest("POST", "/containers/"+ContainerID+"/pause", nil)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	pausedContainers, err := getSliceOfPausedContainers()

	if err != nil {
		c.Fatalf("error thrown while checking if containers were paused: %v", err)
	}

	if len(pausedContainers) != 1 || stringid.TruncateID(ContainerID) != pausedContainers[0] {
		c.Fatalf("there should be one paused container and not %d", len(pausedContainers))
	}

	status, _, err = sockRequest("POST", "/containers/"+ContainerID+"/unpause", nil)
	c.Assert(status, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	pausedContainers, err = getSliceOfPausedContainers()

	if err != nil {
		c.Fatalf("error thrown while checking if containers were paused: %v", err)
	}

	if pausedContainers != nil {
		c.Fatalf("There should be no paused container.")
	}
}

func (s *DockerSuite) TestContainerApiTop(c *check.C) {
	out, err := exec.Command(dockerBinary, "run", "-d", "busybox", "/bin/sh", "-c", "top").CombinedOutput()
	if err != nil {
		c.Fatal(err, out)
	}
	id := strings.TrimSpace(string(out))
	if err := waitRun(id); err != nil {
		c.Fatal(err)
	}

	type topResp struct {
		Titles    []string
		Processes [][]string
	}
	var top topResp
	status, b, err := sockRequest("GET", "/containers/"+id+"/top?ps_args=aux", nil)
	c.Assert(status, check.Equals, http.StatusOK)
	c.Assert(err, check.IsNil)

	if err := json.Unmarshal(b, &top); err != nil {
		c.Fatal(err)
	}

	if len(top.Titles) != 11 {
		c.Fatalf("expected 11 titles, found %d: %v", len(top.Titles), top.Titles)
	}

	if top.Titles[0] != "USER" || top.Titles[10] != "COMMAND" {
		c.Fatalf("expected `USER` at `Titles[0]` and `COMMAND` at Titles[10]: %v", top.Titles)
	}
	if len(top.Processes) != 2 {
		c.Fatalf("expected 2 processes, found %d: %v", len(top.Processes), top.Processes)
	}
	if top.Processes[0][10] != "/bin/sh -c top" {
		c.Fatalf("expected `/bin/sh -c top`, found: %s", top.Processes[0][10])
	}
	if top.Processes[1][10] != "top" {
		c.Fatalf("expected `top`, found: %s", top.Processes[1][10])
	}
}

func (s *DockerSuite) TestContainerApiCommit(c *check.C) {
	cName := "testapicommit"
	out, err := exec.Command(dockerBinary, "run", "--name="+cName, "busybox", "/bin/sh", "-c", "touch /test").CombinedOutput()
	if err != nil {
		c.Fatal(err, out)
	}

	name := "testcommit" + stringid.GenerateRandomID()
	status, b, err := sockRequest("POST", "/commit?repo="+name+"&testtag=tag&container="+cName, nil)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	type resp struct {
		Id string
	}
	var img resp
	if err := json.Unmarshal(b, &img); err != nil {
		c.Fatal(err)
	}

	cmd, err := inspectField(img.Id, "Config.Cmd")
	if err != nil {
		c.Fatal(err)
	}
	if cmd != "{[/bin/sh -c touch /test]}" {
		c.Fatalf("got wrong Cmd from commit: %q", cmd)
	}
	// sanity check, make sure the image is what we think it is
	out, err = exec.Command(dockerBinary, "run", img.Id, "ls", "/test").CombinedOutput()
	if err != nil {
		c.Fatalf("error checking commited image: %v - %q", err, string(out))
	}
}

func (s *DockerSuite) TestContainerApiCreate(c *check.C) {
	config := map[string]interface{}{
		"Image": "busybox",
		"Cmd":   []string{"/bin/sh", "-c", "touch /test && ls /test"},
	}

	status, b, err := sockRequest("POST", "/containers/create", config)
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	type createResp struct {
		Id string
	}
	var container createResp
	if err := json.Unmarshal(b, &container); err != nil {
		c.Fatal(err)
	}

	out, err := exec.Command(dockerBinary, "start", "-a", container.Id).CombinedOutput()
	if err != nil {
		c.Fatal(out, err)
	}
	if strings.TrimSpace(string(out)) != "/test" {
		c.Fatalf("expected output `/test`, got %q", out)
	}
}

func (s *DockerSuite) TestContainerApiCreateWithHostName(c *check.C) {
	var hostName = "test-host"
	config := map[string]interface{}{
		"Image":    "busybox",
		"Hostname": hostName,
	}

	_, b, err := sockRequest("POST", "/containers/create", config)
	if err != nil && !strings.Contains(err.Error(), "200 OK: 201") {
		c.Fatal(err)
	}
	type createResp struct {
		Id string
	}
	var container createResp
	if err := json.Unmarshal(b, &container); err != nil {
		c.Fatal(err)
	}

	var id = container.Id

	_, bodyGet, err := sockRequest("GET", "/containers/"+id+"/json", nil)

	type configLocal struct {
		Hostname string
	}
	type getResponse struct {
		Id     string
		Config configLocal
	}

	var containerInfo getResponse
	if err := json.Unmarshal(bodyGet, &containerInfo); err != nil {
		c.Fatal(err)
	}
	var hostNameActual = containerInfo.Config.Hostname
	if hostNameActual != "test-host" {
		c.Fatalf("Mismatched Hostname, Expected %v, Actual: %v ", hostName, hostNameActual)
	}
}

func (s *DockerSuite) TestContainerApiVerifyHeader(c *check.C) {
	config := map[string]interface{}{
		"Image": "busybox",
	}

	create := func(ct string) (int, io.ReadCloser, error) {
		jsonData := bytes.NewBuffer(nil)
		if err := json.NewEncoder(jsonData).Encode(config); err != nil {
			c.Fatal(err)
		}
		return sockRequestRaw("POST", "/containers/create", jsonData, ct)
	}

	// Try with no content-type
	status, body, err := create("")
	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(err, check.IsNil)
	body.Close()

	// Try with wrong content-type
	status, body, err = create("application/xml")
	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(err, check.IsNil)
	body.Close()

	// now application/json
	status, body, err = create("application/json")
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)
	body.Close()
}

// Issue 7941 - test to make sure a "null" in JSON is just ignored.
// W/o this fix a null in JSON would be parsed into a string var as "null"
func (s *DockerSuite) TestContainerApiPostCreateNull(c *check.C) {
	config := `{
		"Hostname":"",
		"Domainname":"",
		"Memory":0,
		"MemorySwap":0,
		"CpuShares":0,
		"Cpuset":null,
		"AttachStdin":true,
		"AttachStdout":true,
		"AttachStderr":true,
		"PortSpecs":null,
		"ExposedPorts":{},
		"Tty":true,
		"OpenStdin":true,
		"StdinOnce":true,
		"Env":[],
		"Cmd":"ls",
		"Image":"busybox",
		"Volumes":{},
		"WorkingDir":"",
		"Entrypoint":null,
		"NetworkDisabled":false,
		"OnBuild":null}`

	status, body, err := sockRequestRaw("POST", "/containers/create", strings.NewReader(config), "application/json")
	c.Assert(status, check.Equals, http.StatusCreated)
	c.Assert(err, check.IsNil)

	b, err := readBody(body)
	if err != nil {
		c.Fatal(err)
	}
	type createResp struct {
		Id string
	}
	var container createResp
	if err := json.Unmarshal(b, &container); err != nil {
		c.Fatal(err)
	}

	out, err := inspectField(container.Id, "HostConfig.CpusetCpus")
	if err != nil {
		c.Fatal(err, out)
	}
	if out != "" {
		c.Fatalf("expected empty string, got %q", out)
	}
}

func (s *DockerSuite) TestCreateWithTooLowMemoryLimit(c *check.C) {
	config := `{
		"Image":     "busybox",
		"Cmd":       "ls",
		"OpenStdin": true,
		"CpuShares": 100,
		"Memory":    524287
	}`

	status, body, _ := sockRequestRaw("POST", "/containers/create", strings.NewReader(config), "application/json")
	b, err2 := readBody(body)
	if err2 != nil {
		c.Fatal(err2)
	}

	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(strings.Contains(string(b), "Minimum memory limit allowed is 4MB"), check.Equals, true)
}

func (s *DockerSuite) TestStartWithTooLowMemoryLimit(c *check.C) {
	out, _, err := runCommandWithOutput(exec.Command(dockerBinary, "create", "busybox"))
	if err != nil {
		c.Fatal(err, out)
	}

	containerID := strings.TrimSpace(out)

	config := `{
                "CpuShares": 100,
                "Memory":    524287
        }`

	status, body, _ := sockRequestRaw("POST", "/containers/"+containerID+"/start", strings.NewReader(config), "application/json")
	b, err2 := readBody(body)
	if err2 != nil {
		c.Fatal(err2)
	}

	c.Assert(status, check.Equals, http.StatusInternalServerError)
	c.Assert(strings.Contains(string(b), "Minimum memory limit allowed is 4MB"), check.Equals, true)
}

func (s *DockerSuite) TestContainerApiRename(c *check.C) {
	runCmd := exec.Command(dockerBinary, "run", "--name", "first_name", "-d", "busybox", "sh")
	out, _, err := runCommandWithOutput(runCmd)
	c.Assert(err, check.IsNil)

	containerID := strings.TrimSpace(out)
	newName := "new_name" + stringid.GenerateRandomID()
	statusCode, _, err := sockRequest("POST", "/containers/"+containerID+"/rename?name="+newName, nil)

	// 204 No Content is expected, not 200
	c.Assert(statusCode, check.Equals, http.StatusNoContent)
	c.Assert(err, check.IsNil)

	name, err := inspectField(containerID, "Name")
	if name != "/"+newName {
		c.Fatalf("Failed to rename container, expected %v, got %v. Container rename API failed", newName, name)
	}
}
