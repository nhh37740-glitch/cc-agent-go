pipeline {
    agent { label 'media-workspace-agent' }
    options { timestamps(); disableConcurrentBuilds() }
    parameters {
        booleanParam(name: 'DeployDemo', defaultValue: false,
          description: 'Replace the private loopback demo after build and smoke checks')
    }
    stages {
        stage('Verify and package') {
            steps {
                sh '''
                    set -eu
                    sudo docker build --target build \
                      --build-arg SOURCE_COMMIT="$(git rev-parse HEAD)" \
                      --build-arg SOURCE_TREE="$(git rev-parse HEAD^{tree})" \
                      --build-arg SOURCE_DIRTY="$(test -z "$(git status --porcelain)" && echo false || echo true)" \
                      -t "cc-agent-go-build:${BUILD_NUMBER}" .
                    container_id="$(sudo docker create "cc-agent-go-build:${BUILD_NUMBER}")"
                    trap 'sudo docker rm -f "$container_id" >/dev/null' EXIT
                    mkdir -p dist
                    sudo docker cp "$container_id:/src/dist/." dist/
                    python3 scripts/verify_release.py
                '''
                archiveArtifacts artifacts: 'dist/**', fingerprint: true
            }
        }
        stage('Runtime image') {
            steps {
                sh 'sudo docker build -t "cc-agent-go:${BUILD_NUMBER}" .'
            }
        }
        stage('Private page and API smoke') {
            steps {
                sh '''
                    set -eu
                    container_id="$(sudo docker run --detach \
                      --memory 768m --cpus 0.5 --pids-limit 128 \
                      --cap-drop ALL --security-opt no-new-privileges \
                      -e DEEPSEEK_API_KEY=ci-placeholder \
                      "cc-agent-go:${BUILD_NUMBER}")"
                    trap 'sudo docker rm -f "$container_id" >/dev/null 2>&1 || true' EXIT
                    ready=false
                    for attempt in $(seq 1 30); do
                        if sudo docker exec "$container_id" curl --fail --silent \
                          http://127.0.0.1:8080/ >/dev/null 2>&1; then
                            ready=true
                            break
                        fi
                        sleep 2
                    done
                    if [ "$ready" != true ]; then
                        sudo docker logs --tail=100 "$container_id" >&2 || true
                        exit 1
                    fi
                    for route in /harness /council /api/mcp/servers; do
                        sudo docker exec "$container_id" curl --fail --silent \
                          "http://127.0.0.1:8080$route" >/dev/null
                    done
                    api_status="$(sudo docker exec "$container_id" curl --silent \
                      --output /dev/null --write-out '%{http_code}' \
                      --request POST --header 'Content-Type: application/json' \
                      --data '{' http://127.0.0.1:8080/api/chat)"
                    [ "$api_status" = 400 ] || {
                        echo "unexpected /api/chat response: $api_status" >&2
                        exit 1
                    }
                    stream_error="$(sudo docker exec "$container_id" curl --fail --silent \
                      --request POST --header 'Content-Type: application/json' \
                      --data '{' http://127.0.0.1:8080/api/chat/stream)"
                    printf '%s' "$stream_error" | grep -q '"type":"error"'
                '''
            }
        }
        stage('Deploy private demo') {
            when { expression { params.DeployDemo == true } }
            steps {
                sh 'python3 scripts/deploy_demo.py --build-number "$BUILD_NUMBER"'
            }
        }
    }
}
