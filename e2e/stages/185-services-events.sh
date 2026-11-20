# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Services events: the built-in services event source reports a service that starts, stops or
# fails.

stage services-events "a service that stops or fails in the machine is an event on the bus"
pending services-events "blocked: the daemon has no service registry yet for the services event source to watch"
