# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# IDE: the first human opens their own editor (the VSCodium server) through the web door, framed
# only by the interface, closed to anyone not signed in, and installs an extension from Open VSX
# through the edge allow list.

stage ide "the first human opens their own editor through the web door and installs an extension from Open VSX"

pending ide "the IDE servers API is not on main yet (#89); the real check follows when it lands"
