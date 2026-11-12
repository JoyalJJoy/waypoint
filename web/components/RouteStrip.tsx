import type { ShipmentStatus } from "@/lib/types";

type StopState = "done" | "current" | "upcoming" | "failed";

const NORMAL_STEPS: { key: ShipmentStatus; label: string }[] = [
  { key: "pending", label: "Created" },
  { key: "assigned", label: "Assigned" },
  { key: "picked_up", label: "Picked up" },
  { key: "in_transit", label: "In transit" },
  { key: "out_for_delivery", label: "Out" },
  { key: "delivered", label: "Delivered" },
];

interface Props {
  status: ShipmentStatus;
  compact?: boolean;
}

/**
 * The route strip: a courier's journey drawn as a station line. Each stop is
 * a real state from the shipment machine, so the line always matches the data.
 */
export function RouteStrip({ status, compact = false }: Props) {
  const steps = buildSteps(status);

  return (
    <div className={compact ? "route compact" : "route"}>
      {steps.map((step, index) => (
        <div className="stop" data-state={step.state} key={`${step.key}-${index}`}>
          <span className="link" />
          <span className="marker" />
          <span className="name">{step.label}</span>
        </div>
      ))}
    </div>
  );
}

function buildSteps(status: ShipmentStatus) {
  if (status === "cancelled") {
    return [
      { key: "pending", label: "Created", state: "done" as StopState },
      { key: "cancelled", label: "Cancelled", state: "failed" as StopState },
    ];
  }

  if (status === "failed") {
    return [
      { key: "pending", label: "Created", state: "done" as StopState },
      { key: "assigned", label: "Assigned", state: "done" as StopState },
      { key: "picked_up", label: "Picked up", state: "done" as StopState },
      { key: "in_transit", label: "In transit", state: "done" as StopState },
      { key: "failed", label: "Failed", state: "failed" as StopState },
    ];
  }

  const index = NORMAL_STEPS.findIndex((step) => step.key === status);
  return NORMAL_STEPS.map((step, i) => ({
    key: step.key,
    label: step.label,
    state: (i < index ? "done" : i === index ? "current" : "upcoming") as StopState,
  }));
}
