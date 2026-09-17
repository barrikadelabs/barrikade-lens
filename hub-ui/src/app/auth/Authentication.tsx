import { useCallback, useEffect, useMemo, useState } from "react";
import { OrganizationSwitcher, SignIn as ClerkSignIn, SignUp as ClerkSignUp, UserButton, useAuth, useClerk, useOrganization, useOrganizationList } from "@clerk/react";
import { ArrowRight, Radar } from "lucide-react";
import { API, exchangeOIDC, type AuthConfig } from "../../api";
import { resetAnalytics } from "../../analytics";
import { Brand, Failure, Loading } from "../../ui";
import { Shell } from "../shell/Shell";

export function LegacyApplication({ config }: { config: AuthConfig }) {
  const [token, setToken] = useState(() => sessionStorage.getItem("lens-token") ?? "");
  const [authError, setAuthError] = useState("");
  const saveToken = useCallback((value: string) => {
    sessionStorage.setItem("lens-token", value);
    setToken(value);
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const code = params.get("code");
    if (!code) return;
    const verifier = sessionStorage.getItem("lens-pkce-verifier");
    const expected = sessionStorage.getItem("lens-oidc-state");
    const redirect = sessionStorage.getItem("lens-oidc-redirect");
    if (!verifier || !redirect || params.get("state") !== expected) {
      setAuthError("The sign-in state could not be verified.");
      return;
    }
    exchangeOIDC(code, redirect, verifier).then((result) => {
      history.replaceState({}, "", location.pathname);
      saveToken(result.access_token);
    }).catch((error) => setAuthError(String(error)));
  }, [saveToken]);

  if (!token) return <LegacySignIn config={config} onToken={saveToken} authError={authError} />;
  return <Shell api={new API(token)} signOut={() => { resetAnalytics(); sessionStorage.removeItem("lens-token"); setToken(""); }} selfServe={config.self_serve_enabled} />;
}

export function ClerkApplication({ config }: { config: AuthConfig }) {
  const { isLoaded, isSignedIn, getToken } = useAuth();
  const { signOut } = useClerk();
  const { organization } = useOrganization();
  const memberships = useOrganizationList({ userMemberships: { infinite: true } });
  const [bootstrapped, setBootstrapped] = useState("");
  const [workspaceName, setWorkspaceName] = useState("");
  const [error, setError] = useState("");
  const api = useMemo(() => new API(() => getToken()), [getToken]);

  useEffect(() => {
    if (!isLoaded || !isSignedIn || organization || !memberships.isLoaded || bootstrapped === "creating") return;
    setBootstrapped("creating");
    const existing = memberships.userMemberships.data?.[0]?.organization;
    if (!existing) { setBootstrapped("needs-workspace"); return; }
    Promise.resolve(memberships.setActive?.({ organization: existing.id })).catch((reason) => { setError(String(reason)); setBootstrapped(""); });
  }, [bootstrapped, isLoaded, isSignedIn, memberships, organization]);

  useEffect(() => {
    if (!organization || bootstrapped === organization.id) return;
    api.bootstrapWorkspace(organization.name).then(() => setBootstrapped(organization.id)).catch((reason) => setError(String(reason)));
  }, [api, bootstrapped, organization]);

  if (!isLoaded) return <Loading />;
  if (!isSignedIn) return <ManagedSignIn />;
  if (error) return <Failure error={error} retry={() => { setError(""); setBootstrapped(""); }} />;
  if (!organization && bootstrapped === "needs-workspace") return <main className="signin"><section className="signin-story"><Brand /><div className="signin-copy"><span className="product-kicker"><Radar size={14} /> Set up Lens</span><h1>Name your security workspace.</h1><p>This name identifies the organization whose endpoint footprint Lens will assess.</p></div></section><section className="signin-access"><form className="access-card" onSubmit={(event) => { event.preventDefault(); const value = workspaceName.trim(); if (!value) return; setBootstrapped("creating"); Promise.resolve(memberships.createOrganization?.({ name: value })).then((created) => created && memberships.setActive?.({ organization: created.id })).catch((reason) => { setError(String(reason)); setBootstrapped("needs-workspace"); }); }}><p className="eyebrow">WORKSPACE</p><h2>Organization name</h2><label>Name<input value={workspaceName} maxLength={128} required autoFocus onChange={(event) => setWorkspaceName(event.target.value)} placeholder="Acme Security" /></label><button className="button primary full">Create workspace <ArrowRight size={16} /></button></form></section></main>;
  if (!organization || bootstrapped !== organization.id) return <Loading />;
  const organizationControl = <OrganizationSwitcher hidePersonal organizationProfileMode="modal" afterCreateOrganizationUrl="/" afterSelectOrganizationUrl="/" />;
  const userControl = <UserButton userProfileMode="modal" />;
  return <Shell api={api} signOut={() => { resetAnalytics(); return signOut(); }} organizationControl={organizationControl} userControl={userControl} selfServe={config.self_serve_enabled} analyticsConfig={config.analytics} />;
}

function ManagedSignIn() {
  const [signUp, setSignUp] = useState(() => location.hash.includes("sign-up"));
  useEffect(() => {
    const changed = () => setSignUp(location.hash.includes("sign-up"));
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  const appearance = { variables: { colorPrimary: "#ff6b00", colorBackground: "#131313", colorText: "#ffffff", colorInputBackground: "#0c0c0c", colorInputText: "#ffffff" } };
  return <main className="signin managed-signin">
    <section className="signin-story"><Brand /><div className="signin-copy"><span className="product-kicker"><Radar size={14} /> Autonomous agent discovery</span><h1>Bring the agent footprint into focus.</h1><p>Start free, connect the environments you choose, and see evidence-backed results without a score or write access.</p></div></section>
    <section className="signin-access"><div className="managed-auth"><div className="managed-auth-tabs"><button className={!signUp ? "active" : ""} onClick={() => { location.hash = "sign-in"; setSignUp(false); }}>Sign in</button><button className={signUp ? "active" : ""} onClick={() => { location.hash = "sign-up"; setSignUp(true); }}>Create account</button></div>{signUp ? <ClerkSignUp routing="hash" signInUrl="#sign-in" appearance={appearance} /> : <ClerkSignIn routing="hash" signUpUrl="#sign-up" appearance={appearance} />}<p className="auth-legal">{signUp ? <>By creating an account, you agree to the <a href="/terms">design partner terms</a> and acknowledge the <a href="/privacy">Lens privacy notice</a>.</> : <>Managed Lens is governed by the <a href="/terms">design partner terms</a> and <a href="/privacy">privacy notice</a>.</>}</p></div></section>
  </main>;
}
function LegacySignIn({ config, onToken, authError }: { config: AuthConfig; onToken: (token: string) => void; authError: string }) {
  const [value, setValue] = useState("");
  const error = authError;

  const beginOIDC = async () => {
    if (!config?.authorization_endpoint || !config.client_id || !config.redirect_uri) return;
    const verifier = randomURLSafe(64);
    const state = randomURLSafe(24);
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
    sessionStorage.setItem("lens-pkce-verifier", verifier);
    sessionStorage.setItem("lens-oidc-state", state);
    sessionStorage.setItem("lens-oidc-redirect", config.redirect_uri);
    const target = new URL(config.authorization_endpoint);
    target.search = new URLSearchParams({ response_type: "code", client_id: config.client_id, redirect_uri: config.redirect_uri, scope: (config.scopes ?? ["openid"]).join(" "), state, code_challenge: base64URL(new Uint8Array(digest)), code_challenge_method: "S256" }).toString();
    location.assign(target);
  };

  return <main className="signin">
    <section className="signin-story">
      <Brand />
      <div className="signin-copy">
        <span className="product-kicker"><Radar size={14} /> Autonomous agent discovery</span>
        <h1>Bring the agent footprint into focus.</h1>
        <p>Lens gives security and platform leaders a factual map of autonomous systems, where they run, what they connect to, and the evidence behind every conclusion.</p>
      </div>
    </section>
    <section className="signin-access"><div className="access-card">
      <p className="eyebrow">LENS HUB</p><h2>Open your discovery plane</h2>
      <p className="muted">No scores. No enforcement. Just trustworthy organization-wide discovery posture.</p>
      {config?.enabled && <button className="button primary full" onClick={beginOIDC}>Continue with organization SSO <ArrowRight size={16} /></button>}
      {config?.development_bootstrap && <form onSubmit={(event) => { event.preventDefault(); if (value.trim()) onToken(value.trim()); }}>
        <label>Local bootstrap token<input type="password" value={value} onChange={(event) => setValue(event.target.value)} autoFocus={!config?.enabled} /></label>
        <button className="button primary full">Open Lens Hub <ArrowRight size={16} /></button>
      </form>}
      {error && <p className="error-message">{error}</p>}
    </div></section>
  </main>;
}

function randomURLSafe(length: number) { const bytes = crypto.getRandomValues(new Uint8Array(length)); return base64URL(bytes).slice(0, length); }
function base64URL(bytes: Uint8Array) { let value = ""; bytes.forEach((byte) => { value += String.fromCharCode(byte); }); return btoa(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); }
