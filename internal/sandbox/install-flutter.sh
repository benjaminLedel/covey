#!/bin/sh
# Installs the Flutter SDK and fvm into a sandbox image. Run as root, at build
# time, by every workplace that carries Flutter: Dockerfile.sandbox.dev-flutter
# (the role) and Dockerfile.sandbox.dev-full (the all-rounder).
#
# ONE script for both, because the alternative is the same forty lines in two
# files with a version number in each — and the first time somebody bumps one of
# them, two images that claim the same Flutter version stop carrying it. The
# version therefore lives HERE, and a build overrides it through the
# environment (docker build --build-arg FLUTTER_VERSION=…).
#
# What the script cannot own is the PATH and FLUTTER_ROOT: ENV belongs in the
# Dockerfile. Those few lines are repeated, and they are checked by
# TestTheFlutterImagesAgreeOnTheirVersion in internal/sandbox.
set -eu

FLUTTER_VERSION="${FLUTTER_VERSION:-3.47.2}"
FVM_VERSION="${FVM_VERSION:-4.1.2}"

# The SDK comes from the git repository and not from the published tarball, and
# that is not a preference: flutter_infra_release publishes linux archives for
# x64 ONLY. On an arm64 host the tarball is either missing (404) or, worse, x64
# binaries under a name that says nothing about it. The clone works on both,
# because the first `flutter` call fetches the Dart SDK for the architecture it
# is standing on. --depth 1 on the tag: the SDK is a tool here, not a repository
# somebody works in, and its history is several hundred megabytes.
#
# Everything in one script run — that is one image layer — because a `chown -R`
# in a layer of its own would write the whole tree into the image a second time.
#
# It has to be handed over, because the agent is not root and Flutter is not a
# read-only tool: it writes version stamps and its tool cache into the SDK
# directory. `safe.directory` on top of that is belt and braces — Flutter runs
# git inside its own checkout, so any uid that does not match the owner ends
# every `flutter` call at git's "dubious ownership" instead of at a build. The
# chown makes them match today; the line costs nothing and keeps that true if
# the container is ever started under another user.
case "$(dpkg --print-architecture)" in
    amd64) fvm_arch=x64   ;;
    arm64) fvm_arch=arm64 ;;
    *) echo "unsupported architecture: $(dpkg --print-architecture)" >&2; exit 1 ;;
esac

# /opt/flutter belongs to this script, so it takes it rather than assuming it
# is free: a base image that already carries an SDK (dev-full builds on dev, and
# a mis-tagged dev once WAS dev-flutter, #116) would otherwise stop the build at
# "destination path already exists" instead of installing the version asked for.
rm -rf /opt/flutter
git clone --depth 1 --branch "${FLUTTER_VERSION}" \
    https://github.com/flutter/flutter.git /opt/flutter

# fvm beside it, for the project that pins a different version: it fetches that
# one into the agent's home, where it survives every run — for that one
# deviation instead of for everybody. It lies as a tree under /opt/fvm rather
# than as a single binary: the entry point is a shell launcher that expects
# src/dart and src/fvm.snapshot NEXT to it.
#
# Overwritten rather than skipped when it is already there, and the link is
# forced: `dev-full` builds on `dev`, which brings its own fvm along. Installing
# it again costs half a minute and leaves the version this script pins — asking
# "is one already here?" would leave whichever version happened to be there.
tmp="$(mktemp -d)"
curl -fsSL -o "$tmp/fvm.tar.gz" \
    "https://github.com/leoafarias/fvm/releases/download/${FVM_VERSION}/fvm-${FVM_VERSION}-linux-${fvm_arch}.tar.gz"
tar -xzf "$tmp/fvm.tar.gz" -C "$tmp"
rm -rf /opt/fvm
mv "$tmp/fvm" /opt/fvm
ln -sf /opt/fvm/fvm /usr/local/bin/fvm
rm -rf "$tmp"

git config --system --add safe.directory /opt/flutter
/opt/flutter/bin/flutter --version
/opt/flutter/bin/flutter precache --universal --web
chown -R agent:agent /opt/flutter
/opt/fvm/fvm --version
