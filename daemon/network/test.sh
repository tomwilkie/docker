#/bin/bash

set -eux

docker rm $(docker ps -a -q) || true

CONTAINER=$(docker create ubuntu)
NETWORK=$(docker net create --driver=simplebridge)
docker net plug $CONTAINER $NETWORK
docker start $CONTAINER
