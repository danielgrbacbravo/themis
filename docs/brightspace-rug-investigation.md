# RUG Brightspace Authentication and API Investigation

Date: 2026-06-18

Target tenant:

- `https://brightspace.rug.nl`

## Summary

RUG Brightspace can be accessed programmatically without scraping authenticated HTML for the core MVP data surfaces.

Confirmed:

- RUG Brightspace exposes documented Brightspace API routes under `/d2l/api/...`.
- `GET /d2l/api/versions/` is public and returns supported component versions.
- Session cookies from the RUG SAML browser login flow are accepted by documented API routes.
- The following read-only API surfaces returned `200` with a normal learner account:
  - current user
  - my enrollments
  - content modules / table of contents
  - dropbox folders
  - quizzes
  - own grade values

Best long-term authentication path remains official Brightspace OAuth 2.0. Short-term MVP path can use authenticated session cookies.

## Confirmed API Versions

Observed from:

```http
GET https://brightspace.rug.nl/d2l/api/versions/
```

Relevant latest versions:

- `lp`: `1.60`
- `le`: `1.94`

Use the versions endpoint at runtime instead of hardcoding forever.

## Existing Themis Auth Reference

The current Themis integration uses a cookie-backed SSO flow:

1. Start at `GET https://themis.housing.rug.nl/log/in/oidc`.
2. Manually follow redirects through institutional SSO.
3. Preserve cookies across Themis, SURFconext, `signon.rug.nl`, and MFA domains.
4. Parse and replay HTML forms with hidden fields.
5. Submit username/password.
6. Submit TOTP/MFA.
7. Return to Themis.
8. Persist final application cookies.
9. Validate by loading authenticated pages such as `/course/` and `/user`.

No bearer token or OAuth token capture is used by Themis.

## RUG Brightspace Login Flow

Observed Brightspace entry:

```http
GET https://brightspace.rug.nl/d2l/login
```

Redirect chain:

```text
brightspace.rug.nl/d2l/login
-> /d2l/lp/auth/saml/initiate-login?entityId=https://signon.rug.nl/nidp/saml2/metadata
-> https://signon.rug.nl/nidp/saml2/sso?SAMLRequest=...
```

Identity flow:

- SAML via RUG NetIQ/Access Manager at `signon.rug.nl`.
- The Brightspace SAML initializer explicitly references `https://signon.rug.nl/nidp/saml2/metadata`.
- MFA is required after username/password.

Important implementation quirk:

- The first useful login page contains multiple forms.
- The hidden `bbLogin` form is not the user-facing IdP form.
- The correct credential form is `name="IDPLogin"` and contains:
  - `option=credential`
  - `target=https://myuniversity.rug.nl`
  - `Ecom_User_ID`
  - `Ecom_Password`
- The page includes `login_rug.js` and an `onsubmit="return Nestor.setSpLoginFields(this)"` handler, but direct submission of the `IDPLogin` fields worked after the probe selected that form instead of the hidden bridge form.

## Confirmed Session API Behavior

After SAML login and MFA, the script reused the same cookie jar for documented API calls.

Confirmed protected routes:

```http
GET /d2l/api/lp/1.60/users/whoami
GET /d2l/api/lp/1.60/enrollments/myenrollments/
```

Without authentication these routes return Brightspace-style `403 Forbidden`, not `404`, which indicates the API surface exists but requires an authenticated context.

## Course Filtering

`myenrollments` returns organizations, departments, course offerings, groups, and exam shells.

For a useful MVP course list, filter enrollments to:

```text
Access.CanAccess == true
Access.IsActive == true
OrgUnit.Type.Code == "Course Offering"
OrgUnit.HomeUrl != null
```

Exclude:

- `OrgUnit.Type.Code == "Group"`
- `Access.CanAccess == false`
- exam shells with `HomeUrl == null`
- organization / faculty / department containers

## Confirmed MVP Endpoints

Use `lp` and `le` versions discovered from `/d2l/api/versions/`.

Current user:

```http
GET /d2l/api/lp/1.60/users/whoami
```

Enrollments / course list:

```http
GET /d2l/api/lp/1.60/enrollments/myenrollments/
```

Per-course data:

```http
GET /d2l/api/le/1.94/{courseId}/content/toc
GET /d2l/api/le/1.94/{courseId}/content/root/
GET /d2l/api/le/1.94/{courseId}/dropbox/folders/
GET /d2l/api/le/1.94/{courseId}/quizzes/
GET /d2l/api/le/1.94/{courseId}/grades/values/myGradeValues/
```

Observed result shapes:

- `content/toc`: object with `Modules`
- `content/root/`: JSON array
- `dropbox/folders/`: JSON array
- `quizzes/`: object page with `Objects`
- `grades/values/myGradeValues/`: JSON array
- `myenrollments/`: paged result with `Items`

## Probe Results

Sampled five accessible course offerings. Every target endpoint returned `200`.

Examples:

```text
87717 Brightspace Demo Students
- dropbox folders: 1
- quizzes: 2
- content root: 4
- content toc modules: 4

243163 Introduction to Logic (CS)
- my grade values: 7
- content root: 11
- content toc modules: 11
```

This confirms the API can cover the Raycast-style MVP without scraping full HTML.

## Authentication Options

### 1. Official Brightspace OAuth 2.0

Best long-term option.

Expected requirements:

- RUG/Brightspace admin registers an app in Manage Extensibility.
- Authorization Code Grant for user-facing app.
- Redirect URI.
- Scopes for each API route.
- Token lifetime and optional refresh token configuration.

Brightspace OAuth endpoints documented by D2L:

```text
Authorization endpoint: https://auth.brightspace.com/oauth2/auth
Token endpoint:         https://auth.brightspace.com/core/connect/token
```

Likely scopes for MVP:

```text
users:own_profile:read
users:profile:read
enrollment:own_enrollment:read
content:toc:read
content:modules:readonly
dropbox:folders:read
quizzing:quizzes:read
grades:own_grades:read
```

Risk:

- Requires institutional app registration.
- May not be available to a normal student without admin approval.

### 2. Session Cookie Auth Plus Documented API

Best practical short-term option.

Flow:

1. Programmatically complete RUG SAML login and MFA.
2. Reuse the resulting Brightspace session cookies.
3. Call documented `/d2l/api/...` endpoints.

Risk:

- More brittle than OAuth because it depends on web login form behavior.
- MFA must be supplied each fresh login.
- SAML login pages can change.

Benefit:

- Uses documented Brightspace JSON APIs after authentication.
- Avoids full HTML scraping for core data.

### 3. Internal JSON/XHR Endpoints

Fallback only.

Use if documented endpoints lack a specific UI-only object.

Risk:

- Undocumented endpoint changes.
- May depend on UI version and internal anti-CSRF conventions.

### 4. HTML Scraping

Last resort.

Risk:

- Highest brittleness.
- More parsing complexity.
- More likely to break on UI redesigns or localization changes.

## Python Probe

The exploratory probe lives at:

```text
scripts/brightspace_probe.py
```

Useful commands:

```bash
python3 scripts/brightspace_probe.py
python3 scripts/brightspace_probe.py --login
python3 scripts/brightspace_probe.py --login --org-unit-id 457410
python3 scripts/brightspace_probe.py --login --probe-accessible-courses --course-limit 5
python3 scripts/brightspace_probe.py --login --probe-accessible-courses --output-json /tmp/brightspace-probe.json
```

Debugging SAML page behavior:

```bash
python3 scripts/brightspace_probe.py \
  --login \
  --trace-auth \
  --dump-auth-dir /tmp/brightspace-auth
```

Do not pass TOTP on the command line except during quick local debugging; it can land in shell history. Prefer the interactive prompt.

## Recommended Next Implementation Step

Build a small Brightspace client module with these responsibilities:

1. Session login:
   - Start at `/d2l/login`.
   - Follow SAML redirects.
   - Select `IDPLogin` for credentials.
   - Handle MFA field when encountered.
   - Store cookies in a session file.
2. Version discovery:
   - Fetch `/d2l/api/versions/`.
   - Cache relevant `lp` and `le` versions.
3. API client:
   - `WhoAmI`
   - `MyEnrollments`
   - `ListCourses`
   - `GetCourseContentTOC`
   - `GetDropboxFolders`
   - `GetQuizzes`
   - `GetMyGradeValues`
4. CLI/prototype command:
   - Print current courses.
   - For a selected course, print content modules, assignments, quizzes, and grades.

Keep OAuth as the eventual replacement for the SAML login module if RUG app registration becomes available.
