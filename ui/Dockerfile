# Serves the built hyve extension in the layout Rancher's UIPlugin endpoint
# expects (the same as @rancher/shell's publish script makes):
#   /plugin/hyve-<version>/files.txt         list of plugin/... paths
#   /plugin/hyve-<version>/plugin/<files>
# Build: yarn build-pkg hyve && docker build -t hyve-ui:<version> ui
FROM nginxinc/nginx-unprivileged:1.29-alpine
LABEL org.opencontainers.image.source=https://github.com/cbridges1/hyve
COPY --chown=101:101 dist-pkg/ /tmp/dist/
USER root
RUN set -e; cd /tmp/dist; for d in */; do \
      d=${d%/}; out=/usr/share/nginx/html/plugin/$d; \
      mkdir -p $out/plugin; cp -R $d/. $out/plugin/; \
      find $out/plugin \( -name '*.map' -o -name report.html \) -delete; \
      (cd $out && find plugin -type f | sort > files.txt); \
    done; rm -rf /tmp/dist; chown -R 101:101 /usr/share/nginx/html
USER 101
