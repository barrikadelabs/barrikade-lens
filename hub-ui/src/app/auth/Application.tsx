import { useEffect, useState } from "react";
import { ClerkProvider } from "@clerk/react";
import { authConfig, type AuthConfig } from "../../api";
import { Failure, Loading } from "../../ui";
import { DesignPartnerTerms, PrivacyNotice } from "../../Legal";
import { ClerkApplication, LegacyApplication } from "./Authentication";
import { PublicEndpointInstall } from "./PublicEndpointInstall";

export function Application() {
  if (location.pathname === "/install") return <PublicEndpointInstall />;
  if (location.pathname === "/privacy") return <PrivacyNotice />;
  if (location.pathname === "/terms") return <DesignPartnerTerms />;
  const [config, setConfig] = useState<AuthConfig>();
  const [configurationError, setConfigurationError] = useState("");
  useEffect(() => { authConfig().then(setConfig).catch((reason) => setConfigurationError(String(reason))); }, []);
  if (configurationError) return <Failure error={configurationError} retry={() => location.reload()} />;
  if (!config) return <Loading />;
  if (location.protocol === "https:" && config.public_url) {
    const canonical = new URL(`${location.pathname}${location.search}${location.hash}`, config.public_url);
    if (canonical.origin !== location.origin) {
      location.replace(canonical.toString());
      return <Loading />;
    }
  }
  if (config.mode === "clerk" && config.clerk_publishable_key) {
    return <ClerkProvider publishableKey={config.clerk_publishable_key}><ClerkApplication config={config} /></ClerkProvider>;
  }
  return <LegacyApplication config={config} />;
}
