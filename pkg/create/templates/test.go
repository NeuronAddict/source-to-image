package templates

// TestRunScript is a simple test script that verifies the S2I image.
const TestRunScript = `#!/bin/bash
#
# The 'run' performs a simple test that verifies the S2I image.
# The main focus here is to exercise the S2I scripts.
#
# For more information see the documentation:
# https://github.com/openshift/source-to-image/blob/master/docs/builder_image.md
#
# IMAGE_NAME specifies a name of the candidate image used for testing.
# The image has to be available before this script is executed.
#
IMAGE_NAME=${IMAGE_NAME-{{.ImageName}}-candidate}

if [[ ! -z "$(echo $DOCKER_HOST | grep podman)" || ! -z "${FORCE_PODMAN}" ]]
then
  HAS_PODMAN=true
  IMAGE_PREFIX="localhost/"
  DOCKER_BINARY=podman
else
  HAS_PODMAN=false
  DOCKER_BINARY=docker
fi

# Determining system utility executables (darwin compatibility check)
READLINK_EXEC="readlink -zf"
MKTEMP_EXEC="mktemp --suffix=.cid"
if [[ "$OSTYPE" =~ 'darwin' ]]; then
  READLINK_EXEC="readlink"
  MKTEMP_EXEC="mktemp"
  ! type -a "greadlink" &>"/dev/null" || READLINK_EXEC="greadlink"
  ! type -a "gmktemp" &>"/dev/null" || MKTEMP_EXEC="gmktemp"
fi

_dir="$(dirname "${BASH_SOURCE[0]}")"
test_dir="$($READLINK_EXEC ${_dir} || echo ${_dir})"
image_dir=$($READLINK_EXEC ${test_dir}/.. || echo ${test_dir}/..)
scripts_url="${image_dir}/.s2i/bin"
cid_file=$($MKTEMP_EXEC -u)

# Since we built the candidate image locally, we don't want S2I to attempt to pull
# it from Docker hub
s2i_args="--pull-policy=never --loglevel=2"

# Port the image exposes service to be tested
test_port=8080

image_exists() {
  $DOCKER_BINARY inspect $1 &>/dev/null
}

container_exists() {
  image_exists $(cat $cid_file)
}

container_ip() {
  if [[ "${HAS_PODMAN}" == "true" ]]
  then
    ip=$(podman inspect --format="{{"{{"}}(index .HostConfig.PortBindings \"$test_port/tcp\" 0).HostIp {{"}}"}}" $(cat $cid_file) 2>/dev/null | sed 's/0.0.0.0/localhost/')
    [[ -z "${ip}" ]] && echo "localhost" || echo "${ip}"
  else
    docker inspect --format="{{"{{"}}(index .NetworkSettings.Ports \"$test_port/tcp\" 0).HostIp {{"}}"}}" $(cat $cid_file) | sed 's/0.0.0.0/localhost/'
  fi
}

container_port() {
  if [[ "${HAS_PODMAN}" == "true" ]]
  then
    podman inspect --format="{{"{{"}}(index .HostConfig.PortBindings \"$test_port/tcp\" 0).HostPort {{"}}"}}" "$(cat "${cid_file}")"
  else
    docker inspect --format="{{"{{"}}(index .NetworkSettings.Ports \"$test_port/tcp\" 0).HostPort {{"}}"}}" "$(cat "${cid_file}")"
  fi
}

run_s2i_build() {
  if [[ "${HAS_PODMAN}" == "true" ]]
  then
    CONTAINER_FOLDER=$(mktemp -d)
    s2i build --incremental=true ${s2i_args} "${test_dir}"/test-app ${IMAGE_PREFIX}${IMAGE_NAME} ${IMAGE_PREFIX}${IMAGE_NAME} --as-dockerfile "$CONTAINER_FOLDER"/Containerfile
    podman build -t ${IMAGE_PREFIX}${IMAGE_NAME}-testapp -f $CONTAINER_FOLDER/Containerfile $CONTAINER_FOLDER
    rm -fr "$CONTAINER_FOLDER"
  else
    s2i build --incremental=true ${s2i_args} ${test_dir}/test-app ${IMAGE_NAME} ${IMAGE_NAME}-testapp
  fi
}

prepare() {
  if ! image_exists ${IMAGE_PREFIX}${IMAGE_NAME}; then
    echo "ERROR: The image ${IMAGE_PREFIX}${IMAGE_NAME} must exist before this script is executed."
    exit 1
  fi
  # s2i build requires the application is a valid 'Git' repository
  pushd ${test_dir}/test-app >/dev/null
  git init
  git config user.email "build@localhost" && git config user.name "builder"
  git add -A && git commit -m "Sample commit"
  popd >/dev/null
  run_s2i_build
}

run_test_application() {
  $DOCKER_BINARY run --rm --cidfile=${cid_file} -p ${test_port}:${test_port} ${IMAGE_PREFIX}${IMAGE_NAME}-testapp
}

cleanup() {
  if [ -f $cid_file ]; then
    if container_exists; then
      $DOCKER_BINARY stop $(cat $cid_file)
    fi
  fi
  if image_exists ${IMAGE_PREFIX}${IMAGE_NAME}-testapp; then
    $DOCKER_BINARY rmi ${IMAGE_PREFIX}${IMAGE_NAME}-testapp
  fi
}

check_result() {
  local result="$1"
  if [[ "$result" != "0" ]]; then
    echo "S2I image '${IMAGE_NAME}' test FAILED (exit code: ${result})"
    cleanup
    exit $result
  fi
}

wait_for_cid() {
  local max_attempts=10
  local sleep_time=1
  local attempt=1
  local result=1
  while [ $attempt -le $max_attempts ]; do
    [ -f $cid_file ] && break
    echo "Waiting for container to start..."
    attempt=$(( $attempt + 1 ))
    sleep $sleep_time
  done
}

test_usage() {
  echo "Testing 's2i usage'..."
  s2i usage ${s2i_args} ${IMAGE_PREFIX}${IMAGE_NAME} &>/dev/null
}

test_connection() {
  echo "Testing HTTP connection (http://$(container_ip):$(container_port))"
  local max_attempts=10
  local sleep_time=1
  local attempt=1
  local result=1
  while [ $attempt -le $max_attempts ]; do
    echo "Sending GET request to http://$(container_ip):$(container_port)/"
    response_code=$(curl -s -w %{http_code} -o /dev/null http://$(container_ip):$(container_port)/)
    status=$?
    if [ $status -eq 0 ]; then
      if [ $response_code -eq 200 ]; then
        result=0
      fi
      break
    fi
    attempt=$(( $attempt + 1 ))
    sleep $sleep_time
  done
  return $result
}

# Build the application image twice to ensure the 'save-artifacts' and
# 'restore-artifacts' scripts are working properly
prepare
run_s2i_build
check_result $?

# Verify the 'usage' script is working properly
test_usage
check_result $?

# Verify that the HTTP connection can be established to test application container
run_test_application &

# Wait for the container to write its CID file
wait_for_cid

test_connection
check_result $?

cleanup

`

// Makefile contains a sample Makefile which can build and test a container.
const Makefile = `IMAGE_NAME = {{.ImageName}}
DOCKER_BINARY = $(shell /bin/bash -c 'which podman docker | head -1' )

.PHONY: build
build:
	$(DOCKER_BINARY) build -t $(IMAGE_NAME) .

.PHONY: test
test:
	$(DOCKER_BINARY) build -t $(IMAGE_NAME)-candidate .
	IMAGE_NAME=$(IMAGE_NAME)-candidate test/run
`

// Index contains a sample index.html file.
const Index = `<!doctype html>
<html>
	<head>
		<title>Hello World!</title>
	</head>
	<body>
		<h1>Hello World!</h1>
	</body>
</html>
 `
