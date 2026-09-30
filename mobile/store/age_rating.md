# Age rating and category

## Category

| | Category | Why |
|---|---|---|
| Primary | **Business** | The app is part of an organisation's tooling: it signs in to the organisation's covey server with a seat, and what it shows (agents, their questions, colleagues, conversations) exists only inside that organisation. Business is where people look for the companion apps of the systems they use at work. |
| Secondary | **Productivity** | Notes, dictation and meeting transcripts with summaries are the half of the app that is useful to one person, and the half that also works when the organisation has the team surface switched off (the app is then a notetaker). |

`metadata/primary_category.txt` and `metadata/secondary_category.txt` carry these in the form `fastlane deliver` expects (`BUSINESS`, `PRODUCTIVITY`), the same for macOS.

The Mac App Store build additionally needs `LSApplicationCategoryType` in `mobile/macos/Runner/Info.plist` (`public.app-category.business`); it is not set yet.

## Age rating questionnaire

The answers for App Store Connect → **App Information → Age Rating**, from what the app does.

| Question | Answer | Reason |
|---|---|---|
| Violence (cartoon, realistic, graphic) | None | The app shows no such content of its own. |
| Sexual content, nudity | None | |
| Profanity or crude humour | None | |
| Horror or fear themes | None | |
| Alcohol, tobacco, drugs | None | |
| Mature or suggestive themes | None | |
| Simulated gambling, real gambling, contests | None / No | |
| Medical or treatment information, health or wellness topics | None / No | |
| Unrestricted web access | **No** | The app contains no web view or browser. |
| User-generated content | **Yes** | People and agents write messages, visible only to the members of the conversation inside one organisation. Notes are private to the person. Nothing is published. |
| Messaging and chat | **Yes** | Conversations with agents and with colleagues of the same organisation. There is no way to reach people outside it. |
| Advertising | **No** | |
| Parental controls, age assurance | **No** | |

App Store Connect computes the rating from these answers; with no content descriptors set, expect the lowest band, and record the computed value here when it is known. Messaging and user-generated content are answered *Yes* because they exist, even though they are confined to one organisation, and a *No* would describe another app.

Organisations moderate their own content: an administrator removes seats in the web interface, and every seat's API key can be revoked there. Apple asks for a way to report and block users when an app has user-generated content (guideline 1.2); since all content is inside one organisation that administers its own seats, the review notes explain that, and it may still come up in review.
