"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { RouteStrip } from "@/components/RouteStrip";
import { StatusChip } from "@/components/StatusChip";
import { ApiError, publicTracking, trackingSocketUrl } from "@/lib/api";
import { shipmentColor, stamp } from "@/lib/format";
import type { PublicTracking } from "@/lib/types";

export default function TrackPage() {
  const params = useParams<{ tracking: string }>();
  const trackingNumber = decodeURIComponent(params.tracking ?? "");

  const [tracking, setTracking] = useState<PublicTracking | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [live, setLive] = useState(false);
  const socketRef = useRef<WebSocket | null>(null);

  const load = useCallback(async () => {
    try {
      const snapshot = await publicTracking(trackingNumber);
      setTracking(snapshot);
      setError(null);
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 404
          ? "We could not find that tracking number."
          : "Something went wrong while loading the shipment.",
      );
    }
  }, [trackingNumber]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!trackingNumber) return;

    let socket: WebSocket | null = null;
    let closed = false;

    try {
      socket = new WebSocket(trackingSocketUrl(trackingNumber));
      socketRef.current = socket;
    } catch {
      return;
    }

    socket.onopen = () => setLive(true);
    socket.onclose = () => setLive(false);
    socket.onerror = () => setLive(false);
    socket.onmessage = () => {
      // The push only says "something changed"; the snapshot is the source of
      // truth, so pull it again.
      void load();
    };

    return () => {
      closed = true;
      socket?.close();
      socketRef.current = null;
      if (!closed) setLive(false);
    };
  }, [trackingNumber, load]);

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
        {error ? (
          <div className="track-card">
            <p className="eyebrow">Tracking</p>
            <h1 className="track-status">Not found</h1>
            <p className="muted">{error}</p>
            <Link className="btn" href="/track" style={{ marginTop: 16 }}>
              Try another number
            </Link>
          </div>
        ) : null}

        {tracking ? (
          <div className="track-card">
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <p className="eyebrow">Tracking number</p>
              {live ? (
                <span className="eyebrow" style={{ color: "var(--teal)", display: "flex", gap: 8, alignItems: "center" }}>
                  <span className="live-dot" /> live
                </span>
              ) : null}
            </div>
            <p className="tracking-number">{tracking.tracking_number}</p>

            <h1 className="track-status">{tracking.status.replace(/_/g, " ")}</h1>
            <p className="muted" style={{ marginTop: 8 }}>
              {tracking.carrier} · {tracking.service_level} · {tracking.order_number}
            </p>

            <div style={{ margin: "26px 0 6px" }}>
              <RouteStrip status={tracking.status} />
            </div>

            <dl className="kv" style={{ marginTop: 24 }}>
              <dt>Destination</dt>
              <dd>
                {tracking.destination.city}
                {tracking.destination.region ? `, ${tracking.destination.region}` : ""} ·{" "}
                {tracking.destination.country}
              </dd>
              {tracking.estimated_delivery_at ? (
                <>
                  <dt>Estimated</dt>
                  <dd className="mono">{stamp(tracking.estimated_delivery_at)}</dd>
                </>
              ) : null}
              {tracking.picked_up_at ? (
                <>
                  <dt>Picked up</dt>
                  <dd className="mono">{stamp(tracking.picked_up_at)}</dd>
                </>
              ) : null}
              {tracking.delivered_at ? (
                <>
                  <dt>Delivered</dt>
                  <dd className="mono">{stamp(tracking.delivered_at)}</dd>
                </>
              ) : null}
            </dl>

            <p className="eyebrow" style={{ marginTop: 26 }}>
              Journey
            </p>
            <ul className="timeline">
              {tracking.events.map((event, index) => (
                <li key={`${event.created_at}-${index}`}>
                  <span className="when">{stamp(event.created_at)}</span>
                  <span>
                    <strong style={{ fontWeight: 500 }}>{event.description}</strong>
                    <div style={{ marginTop: 2 }}>
                      <StatusChip
                        status={event.status}
                        color={shipmentColor(event.status)}
                        label={event.status.replace(/_/g, " ")}
                      />
                    </div>
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {!tracking && !error ? (
          <div className="track-card">
            <span className="spinner" />
          </div>
        ) : null}
      </main>
    </div>
  );
}
