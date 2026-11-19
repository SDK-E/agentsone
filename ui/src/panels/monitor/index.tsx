// SPDX-License-Identifier: MIT
import { AgentRows } from "./agent-rows";
import { BudgetCard } from "./budget-card";
import { MachineCard } from "./machine-card";

/** Monitor: the budget, the machine and every agent now, with the live society stream. */
export default function MonitorPanel() {
  return (
    <div className="monitor">
      <div className="monitor__top">
        <BudgetCard />
        <MachineCard />
      </div>
      <AgentRows />
    </div>
  );
}
