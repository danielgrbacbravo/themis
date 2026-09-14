# Brightspace Raycast Extension Plan

Date: 2026-06-18

Goal:

- Build a Raycast-style Brightspace integration that can support RUG first, but is not hardcoded to RUG's SAML flow.

## Core Principle

Brightspace APIs are mostly standardized. University authentication is not.

Do not put institution-specific login behavior inside the Brightspace API client. Keep authentication behind a provider interface, then let the rest of the extension call documented `/d2l/api/...` routes.

Recommended architecture:

```text
Raycast extension
-> Brightspace client
   -> Auth provider
      -> OAuth provider
      -> Browser session provider
      -> Institution-specific provider
   -> D2L API client
```

## Authentication Strategy

### 1. Official Brightspace OAuth 2.0

Best long-term option.

Use when the institution supports app registration through Brightspace Manage Extensibility.

Benefits:

- Standard D2L-supported authentication.
- Works across tenants if the app is registered/approved.
- Uses bearer tokens rather than web-session cookies.
- Can support refresh tokens.
- Avoids SAML/OIDC/MFA form emulation.

Known Brightspace OAuth endpoints:

```text
Authorization endpoint: https://auth.brightspace.com/oauth2/auth
Token endpoint:         https://auth.brightspace.com/core/connect/token
```

Likely MVP scopes:

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

- Requires institutional admin/app registration.
- A normal student may not be able to self-register a production app.

### 2. Browser Session Provider

Best generic fallback if Raycast can support it.

Flow:

1. Ask the user for their Brightspace tenant URL.
2. Open the tenant login page in a browser/WebView.
3. Let the user complete the institution's real SSO/MFA flow.
4. Capture Brightspace session cookies after login.
5. Validate with `GET /d2l/api/lp/{version}/users/whoami`.
6. Store session state in secure local storage.
7. Use cookies for documented API requests.

Benefits:

- Avoids implementing SAML, OIDC, Microsoft, Okta, Duo, SURFconext, NetIQ, Shibboleth, and other IdP variants.
- Lets the university's own login page handle MFA and conditional access.
- Still uses documented Brightspace APIs after authentication.

Risk:

- Raycast may not expose a clean way to capture browser cookies from an external browser.
- Session cookies expire.
- Some tenants may bind sessions to browser/device signals.

Fallback if Raycast cannot capture cookies:

- Keep OAuth as the public/default auth path.
- Use institution-specific auth only for private/local workflows.
- Consider a companion local login helper.
- As an advanced debug mode only, allow manual cookie paste/import.

### 3. Institution-Specific Provider

Use for known institutions when OAuth is unavailable and browser-session capture is not practical.

RUG provider:

- Start at `/d2l/login`.
- Follow Brightspace SAML redirect.
- Use `signon.rug.nl`.
- Select the `IDPLogin` form.
- Submit `Ecom_User_ID`, `Ecom_Password`, and MFA field when prompted.
- Reuse final Brightspace cookies for `/d2l/api/...`.

Risk:

- Fragile.
- Login form, MFA, or JavaScript behavior can change.
- Must be maintained per institution.

### 4. HTML Scraping

Last resort.

Only use if documented APIs lack a necessary object.

Risk:

- Highest brittleness.
- Localization and UI changes can break parsing.
- More maintenance than JSON API routes.

## Tenant Discovery

Ask the user for a Brightspace URL:

```text
https://brightspace.rug.nl
https://youruni.brightspace.com
https://learn.university.edu
```

Normalize to a base URL, then probe:

```http
GET {tenant}/d2l/api/versions/
```

Interpretation:

- `200`: Brightspace API is present.
- `404`: wrong URL, non-Brightspace target, or unusual deployment.
- network/TLS error: invalid URL or connectivity problem.

Then probe authentication:

```http
GET {tenant}/d2l/api/lp/{lpVersion}/users/whoami
```

Interpretation:

- `200`: authenticated context works.
- `401` or `403`: API exists; authentication required or invalid.
- `404`: wrong API version or route issue.

## Provider Interface

Conceptual TypeScript shape:

```ts
interface BrightspaceAuthProvider {
  authenticate(): Promise<AuthState>;
  refresh?(state: AuthState): Promise<AuthState>;
  getRequestHeaders(state: AuthState): Promise<Record<string, string>>;
}
```

Auth state:

```ts
type AuthState =
  | {
      kind: "oauth";
      tenantUrl: string;
      accessToken: string;
      refreshToken?: string;
      expiresAt: string;
    }
  | {
      kind: "session";
      tenantUrl: string;
      cookies: Cookie[];
      createdAt: string;
    };
```

Auth modes:

```ts
type AuthMode =
  | { kind: "oauth"; clientId: string; clientSecret?: string; redirectUri: string }
  | { kind: "browserSession"; tenantUrl: string }
  | { kind: "custom"; providerId: "rug"; tenantUrl: string };
```

## Brightspace API Client

The API client should not know how authentication happened. It should receive request headers from the auth provider.

Core methods:

```ts
class BrightspaceClient {
  getVersions(): Promise<ApiVersions>;
  whoAmI(): Promise<User>;
  myEnrollments(): Promise<MyEnrollment[]>;
  listCourses(): Promise<CourseOffering[]>;
  getContentToc(courseId: string): Promise<ContentToc>;
  getContentRoot(courseId: string): Promise<ContentObject[]>;
  getDropboxFolders(courseId: string): Promise<DropboxFolder[]>;
  getQuizzes(courseId: string): Promise<Quiz[]>;
  getMyGradeValues(courseId: string): Promise<GradeValue[]>;
}
```

Useful confirmed routes:

```http
GET /d2l/api/versions/
GET /d2l/api/lp/{lpVersion}/users/whoami
GET /d2l/api/lp/{lpVersion}/enrollments/myenrollments/
GET /d2l/api/le/{leVersion}/{courseId}/content/toc
GET /d2l/api/le/{leVersion}/{courseId}/content/root/
GET /d2l/api/le/{leVersion}/{courseId}/dropbox/folders/
GET /d2l/api/le/{leVersion}/{courseId}/quizzes/
GET /d2l/api/le/{leVersion}/{courseId}/grades/values/myGradeValues/
```

## Course Filtering

`myenrollments` contains organizations, departments, groups, inaccessible exam shells, and normal courses.

Use:

```text
Access.CanAccess == true
Access.IsActive == true
OrgUnit.Type.Code == "Course Offering"
OrgUnit.HomeUrl != null
```

Skip:

- groups
- organization/faculty/department containers
- inaccessible exam shells
- entries without `HomeUrl`

## MVP Feature Set

Initial Raycast commands:

1. `Brightspace: Login`
   - configure tenant URL
   - run selected auth provider
   - validate `whoami`

2. `Brightspace: My Courses`
   - fetch `myenrollments`
   - show filtered course offerings
   - include last accessed, code, and name

3. `Brightspace: Course Content`
   - select course
   - fetch `content/toc`
   - show modules/topics

4. `Brightspace: Assignments`
   - select course
   - fetch `dropbox/folders`
   - show due dates and availability

5. `Brightspace: Quizzes`
   - select course
   - fetch `quizzes`
   - show quiz availability/dates when present

6. `Brightspace: Grades`
   - select course
   - fetch `grades/values/myGradeValues`
   - show own grade values

## Security Notes

- Avoid collecting passwords in the extension when OAuth or browser login is possible.
- Do not log usernames, passwords, MFA codes, cookies, SAML requests, SAML responses, or bearer tokens.
- Store tokens/cookies only in Raycast secure storage or platform keychain.
- Treat session-cookie auth as sensitive as a password-equivalent session.
- Prefer short-lived session storage and explicit logout/clear-session command.

## Recommended Build Order

1. Build the versioned Brightspace API client.
2. Port the proven RUG session auth into a `RugSessionAuthProvider`.
3. Implement `listCourses` from `myenrollments`.
4. Implement per-course content/dropbox/quizzes/grades commands.
5. Add an OAuth auth provider.
6. Investigate whether Raycast can support browser-session auth cleanly.
7. If browser-session auth is not viable, keep institution-specific providers isolated and optional.

## Non-Goals For MVP

- Submitting assignments.
- Changing account settings.
- Posting quiz attempts.
- Writing grades/content.
- Scraping HTML pages for data already available through documented APIs.
