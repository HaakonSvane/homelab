// Passkey glue — the only custom JavaScript in the app. A button with
// data-passkey="login" or "register" (and optionally data-token) runs the WebAuthn
// ceremony against /passkey/<mode>/begin and /finish, then follows the redirect.
const post = async (path, body) => {
  const res = await fetch(path, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(json.error || "Noe gikk galt. Prøv igjen.");
  return json;
};

for (const button of document.querySelectorAll("[data-passkey]")) {
  button.addEventListener("click", async () => {
    const mode = button.dataset.passkey;
    const error = document.getElementById("passkey-error");
    error.hidden = true;
    button.disabled = true;
    try {
      if (!window.PublicKeyCredential?.parseRequestOptionsFromJSON) throw new Error("Nettleseren støtter ikke passkeys. Oppdater den og prøv igjen.");
      const begin = await post(`/passkey/${mode}/begin`, {token: button.dataset.token || ""});
      const credential = mode === "login"
        ? await navigator.credentials.get({publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(begin.publicKey)})
        : await navigator.credentials.create({publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(begin.publicKey)});
      const done = await post(`/passkey/${mode}/finish`, credential.toJSON());
      location.assign(done.redirect);
    } catch (e) {
      error.textContent = e.name === "NotAllowedError" ? "Avbrutt eller tidsavbrudd. Prøv igjen."
        : e.name === "InvalidStateError" ? "Denne enheten har allerede en passkey for deg." : e.message;
      error.hidden = false;
      button.disabled = false;
    }
  });
}
