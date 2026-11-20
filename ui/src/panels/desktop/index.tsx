// SPDX-License-Identifier: MIT
import { useMe } from "../../session/session";
import { connectNoVnc } from "./connection";
import { DesktopView } from "./desktop-view";

/** The signed-in human's own desktop, shown in the browser. */
export default function DesktopPanel() {
  const me = useMe();
  if (me.data?.kind !== "human") return null;
  return <DesktopView human={me.data.human.name} connect={connectNoVnc} />;
}
