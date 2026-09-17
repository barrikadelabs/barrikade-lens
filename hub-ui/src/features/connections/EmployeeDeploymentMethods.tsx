import { Building2, Laptop, Network, TerminalSquare } from "lucide-react";
import type { DeploymentMethod } from "./connection-options";

const methods: Array<{ id: DeploymentMethod; title: string; detail: string; icon: typeof Laptop }> = [
  { id: "this_computer", title: "Install on this computer", detail: "Start with the device you are using now", icon: Laptop },
  { id: "company", title: "Deploy across my company", detail: "Create a reusable rollout path for your fleet", icon: Building2 },
  { id: "command_line", title: "Command line", detail: "Generate a short-lived installation command", icon: TerminalSquare },
  { id: "mdm", title: "Device management / MDM", detail: "Prepare instructions for Jamf, Intune, or another MDM", icon: Network },
];

export function EmployeeDeploymentMethods({ selected, onSelect }: { selected?: DeploymentMethod; onSelect: (method: DeploymentMethod) => void }) {
  return <div className="deployment-methods" role="radiogroup" aria-label="Employee device deployment method">
    {methods.map(({ id, title, detail, icon: Icon }) => <button key={id} role="radio" aria-checked={selected === id} className={selected === id ? "active" : ""} onClick={() => onSelect(id)}>
      <Icon size={19} /><span><b>{title}</b><small>{detail}</small></span>
    </button>)}
  </div>;
}
