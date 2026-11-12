import { SignInForm } from "@/components/SignInForm";

export default function LoginPage() {
  return (
    <div className="login-wrap">
      <SignInForm
        nextPath="/console"
        title="Waypoint console"
        lede="Sign in with a demo account. Credentials are listed on the home page."
      />
    </div>
  );
}
