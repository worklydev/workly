# The release image. goreleaser builds it from the release binaries (see
# dockers_v2 in .goreleaser.yaml), so the image runs the same binary as
# GitHub releases and PyPI. Build it locally with `just docker`.
FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/workly /usr/local/bin/workly

# `workly dev` keeps its SQLite database in .workly/ under the working directory.
WORKDIR /home/nonroot
# Numeric, so Kubernetes can verify runAsNonRoot: distroless's nonroot user.
USER 65532:65532
EXPOSE 7337
# For Docker and Compose; Kubernetes ignores it in favour of its own probes.
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s CMD ["workly", "healthcheck"]
ENTRYPOINT ["workly"]
CMD ["serve"]
