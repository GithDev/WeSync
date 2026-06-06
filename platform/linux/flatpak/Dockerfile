# WeSync Flatpak builder toolchain.
#
# Separate from the wesync-linux image on purpose: this step compiles NOTHING
# (no Go), it only packages the prebuilt binaries into a flatpak. Keeps the Go
# image lean and this image focused on flatpak/flatpak-builder.
#
# Must run with --privileged (flatpak-builder uses bubblewrap, which needs user
# namespaces); the Makefile's linux-flatpak target adds that flag. The GNOME
# runtime/SDK are large (~1.5 GB) — cache them in a named volume mounted at
# /root/.local/share/flatpak (see the Makefile) so they download only once.
#
# Build:  podman build -f platform/linux/flatpak/Dockerfile -t wesync-flatpak .
FROM docker.io/library/ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        flatpak flatpak-builder ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY platform/linux/flatpak/build-flatpak.sh /usr/local/bin/build-flatpak.sh
RUN sed -i 's/\r$//' /usr/local/bin/build-flatpak.sh && \
    chmod +x /usr/local/bin/build-flatpak.sh

ENTRYPOINT ["/usr/local/bin/build-flatpak.sh"]
