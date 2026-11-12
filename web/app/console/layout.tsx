"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useState } from "react";
import { SignInForm } from "@/components/SignInForm";
import { logout } from "@/lib/api";
import { useSession } from "@/lib/useSession";

const nav = [
  { href: "/console", label: "Order board" },
  { href: "/console/shipments", label: "Shipments" },
  { href: "/console/inventory", label: "Inventory" },
  { href: "/agent", label: "Agent view" },
];

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { session, loading, reload } = useSession();
  const [signingOut, setSigningOut] = useState(false);

  async function signOut() {
    setSigningOut(true);
    try {
      await logout();
    } catch {
      // ignore: the cookie is cleared regardless
    }
    router.push("/");
    router.refresh();
  }

  if (loading) {
    return (
      <div className="shell">
        <header className="topbar">
          <span className="wordmark">
            <span className="beacon" />
            Waypoint
          </span>
        </header>
        <div className="main">
          <span className="spinner" />
        </div>
      </div>
    );
  }

  if (!session) {
    return (
      <div className="login-wrap">
        <SignInForm
          nextPath="/console"
          title="Waypoint console"
          lede="Sign in to the dispatch console. Demo accounts are listed on the home page."
          onSignedIn={reload}
        />
      </div>
    );
  }

  // Couriers work from the agent console, not the dispatch board. Saying so
  // plainly beats a screen full of permission errors.
  if (session.tenant.role === "delivery_agent") {
    return (
      <div className="shell">
        <header className="topbar">
          <span className="wordmark">
            <span className="beacon" />
            Waypoint
          </span>
          <span className="spacer" />
          <button className="btn btn-sm" onClick={signOut} disabled={signingOut}>
            Sign out
          </button>
        </header>
        <main className="main">
          <div className="notice" style={{ maxWidth: 620 }}>
            <strong style={{ fontWeight: 600 }}>This is the operations console.</strong>
            <p className="muted" style={{ margin: "8px 0 16px" }}>
              Your account is a delivery agent, so it can read shipments but not orders or
              stock. Your own route lives in the agent console.
            </p>
            <Link className="btn btn-primary" href="/agent">
              Open the agent console
            </Link>
          </div>
        </main>
      </div>
    );
  }

  return (
    <div className="shell">
      <header className="topbar">
        <span className="wordmark">
          <span className="beacon" />
          Waypoint
        </span>
        <span className="mono faint" style={{ fontSize: 12 }}>
          {session.tenant.tenant_name} · {session.tenant.role.replace(/_/g, " ")}
        </span>
        <span className="spacer" />
        <span className="mono faint" style={{ fontSize: 12 }}>
          {session.user.email}
        </span>
        <button className="btn btn-sm" onClick={signOut} disabled={signingOut}>
          Sign out
        </button>
      </header>

      <div className="body-grid">
        <nav className="rail">
          <p className="eyebrow rail-label">Operations</p>
          {nav.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              data-active={
                item.href === "/console" ? pathname === "/console" : pathname.startsWith(item.href)
              }
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <main className="main">{children}</main>
      </div>
    </div>
  );
}
