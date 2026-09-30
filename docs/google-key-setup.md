# Generar un Google Gmail OAuth Refresh Token

Aquest procediment serveix per obtenir manualment:

```text
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
GOOGLE_REFRESH_TOKEN
```

per accedir a Gmail des d'un backend sense haver de tornar a autenticar l'usuari cada vegada.

## 1. Crear o seleccionar el projecte a Google Cloud

Entrar a Google Cloud Console:

```text
https://console.cloud.google.com/
```

Seleccionar el projecte que utilitzarà la integració.

---

## 2. Activar Gmail API

Anar a:

```text
APIs & Services
→ Library
```

Buscar:

```text
Gmail API
```

i prémer:

```text
Enable
```

---

## 3. Configurar Google Auth Platform / OAuth consent screen

Anar a:

```text
Google Auth Platform
→ Branding
```

o, segons la UI de Google:

```text
APIs & Services
→ OAuth consent screen
```

Configurar com a mínim:

```text
App name
User support email
Developer contact email
```

Per una aplicació personal, seleccionar:

```text
Audience: External
```

Si l'aplicació està en mode:

```text
Testing
```

afegir el compte de Gmail que utilitzaràs a:

```text
Audience
→ Test users
→ Add users
```

IMPORTANT:

Si l'app està en `Testing`, els refresh tokens obtinguts amb scopes de Gmail expiren als **7 dies**.

Per tenir un token persistent, l'app haurà d'estar en:

```text
In production
```

Google documenta explícitament aquesta limitació dels 7 dies per aplicacions External en mode Testing.

---

## 4. Crear un OAuth Client

Anar a:

```text
Google Auth Platform
→ Clients
```

o:

```text
APIs & Services
→ Credentials
→ Create credentials
→ OAuth client ID
```

Seleccionar:

```text
Application type: Web application
```

Nom, per exemple:

```text
Amelia Gmail OAuth
```

A:

```text
Authorized redirect URIs
```

afegir exactament:

```text
https://developers.google.com/oauthplayground
```

Guardar.

Google donarà:

```text
Client ID
Client Secret
```

Guardar-los:

```env
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
```

---

## 5. Obrir OAuth 2.0 Playground

Anar a:

```text
https://developers.google.com/oauthplayground
```

Obrir la configuració amb la icona de l'engranatge.

Configurar:

```text
OAuth flow: Server-side
Access type: Offline
Force prompt: Consent Screen
```

Activar:

```text
Use your own OAuth credentials
```

Introduir:

```text
OAuth Client ID: <GOOGLE_CLIENT_ID>
OAuth Client secret: <GOOGLE_CLIENT_SECRET>
```

---

## 6. Seleccionar els scopes de Gmail

A l'esquerra, introduir manualment els scopes necessaris.

Per llegir correus:

```text
https://www.googleapis.com/auth/gmail.readonly
```

Per crear i modificar drafts:

```text
https://www.googleapis.com/auth/gmail.compose
```

Per enviar correus:

```text
https://www.googleapis.com/auth/gmail.send
```

Per la nostra integració, podem posar:

```text
https://www.googleapis.com/auth/gmail.readonly
https://www.googleapis.com/auth/gmail.compose
https://www.googleapis.com/auth/gmail.send
```

Després prémer:

```text
Authorize APIs
```

---

## 7. Autoritzar el compte de Gmail

Google mostrarà la pantalla de login.

Seleccionar el compte que volem utilitzar.

Pot aparèixer:

```text
Google hasn't verified this app
```

si l'aplicació és pròpia/no verificada.

En aquest cas:

```text
Advanced
→ Go to <nom de l'app>
```

Acceptar els permisos.

Google tornarà automàticament a OAuth Playground.

---

## 8. Intercanviar l'authorization code pels tokens

Ara OAuth Playground estarà al:

```text
Step 2
Exchange authorization code for tokens
```

Prémer:

```text
Exchange authorization code for tokens
```

La resposta contindrà aproximadament:

```json
{
  "access_token": "...",
  "expires_in": 3599,
  "refresh_token": "...",
  "scope": "...",
  "token_type": "Bearer"
}
```

El que ens interessa conservar és:

```text
refresh_token
```

Guardar-lo:

```env
GOOGLE_REFRESH_TOKEN=...
```

---

## 9. Variables finals

Al backend tindrem:

```env
GOOGLE_CLIENT_ID=xxxxxxxx.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=xxxxxxxx
GOOGLE_REFRESH_TOKEN=1//xxxxxxxx
```

No cal guardar manualment l'`access_token`.

L'`access_token` és temporal i el backend el pot regenerar sempre a partir del `refresh_token`.

---

## 10. Obtenir un access token amb el refresh token

La petició és:

```bash
curl -X POST https://oauth2.googleapis.com/token \
  -d client_id="$GOOGLE_CLIENT_ID" \
  -d client_secret="$GOOGLE_CLIENT_SECRET" \
  -d refresh_token="$GOOGLE_REFRESH_TOKEN" \
  -d grant_type=refresh_token
```

La resposta serà semblant a:

```json
{
  "access_token": "ya29....",
  "expires_in": 3599,
  "scope": "...",
  "token_type": "Bearer"
}
```

Aquest `access_token` és el que s'utilitza per cridar Gmail API:

```http
Authorization: Bearer ACCESS_TOKEN
```

---

# Problemes habituals

## No apareix `refresh_token`

Normalment passa perquè Google ja havia autoritzat anteriorment aquest client.

Comprovar al Playground:

```text
Access type: Offline
Force prompt: Consent Screen
```

i tornar a autoritzar.

---

## `redirect_uri_mismatch`

Comprovar que al OAuth Client hi hagi exactament:

```text
https://developers.google.com/oauthplayground
```

No pot haver-hi cap diferència.

---

## El token deixa de funcionar als 7 dies

Comprovar:

```text
Google Auth Platform
→ Audience
→ Publishing status
```

Si posa:

```text
Testing
```

els refresh tokens de Gmail expiren després de 7 dies.

Cal passar l'aplicació a:

```text
In production
```

si volem un token de llarga durada.

---

## Variables a conservar

Només necessitem conservar permanentment:

```env
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GOOGLE_REFRESH_TOKEN=
```

L'`access_token` es genera automàticament quan cal.