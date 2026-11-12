"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";

export default function TrackLookupPage() {
  const router = useRouter();
  const [value, setValue] = useState("");

  return (
    <div className="track-shell">
      <header className="topbar">
        <Link className="wordmark" href="/">
          <span className="beacon" />
          Waypoint
        </Link>
        <span className="spacer" />
        <span className="eyebrow">Public tracking</span>
      </header>

      <main className="track-main map-grid">
        <div className="track-card">
          <p className="eyebrow">Track a shipment</p>
          <h1 className="track-status" style={{ fontSize: 28 }}>
            Where is my parcel?
          </h1>
          <p className="muted" style={{ marginTop: 8 }}>
            Enter the tracking number from your confirmation email. No account needed.
          </p>

          <form
            style={{ marginTop: 22 }}
            onSubmit={(event) => {
              event.preventDefault();
              const tracking = value.trim().toUpperCase();
              if (tracking) router.push(`/track/${encodeURIComponent(tracking)}`);
            }}
          >
            <label className="field">
              <span className="label">Tracking number</span>
              <input
                className="mono"
                value={value}
                onChange={(event) => setValue(event.target.value)}
                placeholder="WP…"
                autoFocus
                required
              />
            </label>
            <button className="btn btn-primary" type="submit" style={{ width: "100%" }}>
              Track shipment
            </button>
          </form>
        </div>
      </main>
    </div>
  );
}
