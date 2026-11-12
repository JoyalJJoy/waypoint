"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { ApiError, login } from "@/lib/api";

interface Props {
  nextPath: string;
  title?: string;
  lede?: string;
  onSignedIn?: () => void;
}

export function SignInForm({
  nextPath,
  title = "Sign in",
  lede = "Use a demo account to open the console.",
  onSignedIn,
}: Props) {
  const router = useRouter();
  const [email, setEmail] = useState("dispatcher@northwind.test");
  const [password, setPassword] = useState("Passw0rd!");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(email, password);
      // The session cookie is now set, so pull the profile again before the
      // router moves on. Without this the page would still show the form.
      await onSignedIn?.();
      router.push(nextPath);
      router.refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "could not sign in");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="login-card" onSubmit={submit}>
      <h1>{title}</h1>
      <p className="muted" style={{ margin: "0 0 20px", fontSize: 14 }}>
        {lede}
      </p>

      <label className="field">
        <span className="label">Email</span>
        <input
          type="email"
          value={email}
          autoComplete="username"
          onChange={(event) => setEmail(event.target.value)}
          required
        />
      </label>

      <label className="field">
        <span className="label">Password</span>
        <input
          type="password"
          value={password}
          autoComplete="current-password"
          onChange={(event) => setPassword(event.target.value)}
          required
        />
      </label>

      {error ? (
        <p className="notice bad" style={{ marginBottom: 14 }}>
          {error}
        </p>
      ) : null}

      <button className="btn btn-primary" type="submit" disabled={busy} style={{ width: "100%" }}>
        {busy ? <span className="spinner" /> : "Sign in"}
      </button>
    </form>
  );
}
