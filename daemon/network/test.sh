#/bin/bash

set -eux

docker rm $(docker ps -a -q)
CONTAINER=$(docker create ubuntu)
NETWORK=$(docker net create --driver=noop)
docker net plug $CONTAINER $NETWORK
docker start $CONTAINER
