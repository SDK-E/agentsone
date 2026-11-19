// SPDX-License-Identifier: MIT

package echo_test

import "encoding/json"

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
