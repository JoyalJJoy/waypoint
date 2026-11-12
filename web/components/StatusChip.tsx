import { humanStatus } from "@/lib/format";

interface Props {
  status: string;
  color: string;
  label?: string;
}

export function StatusChip({ status, color, label }: Props) {
  return (
    <span className="chip" style={{ ["--chip" as string]: color }}>
      <span className="dot" />
      {label ?? humanStatus(status)}
    </span>
  );
}
