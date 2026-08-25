# Dorm Memorial Community — Design System

> Global source of truth. Page-specific rules in `pages/` override this file.

## Design direction

- Style: warm editorial scrapbook with restrained analog-photo cues.
- Mood: personal, calm, familiar, slightly nostalgic; never childish or overly decorative.
- Structure: clear social-product navigation combined with magazine-like content presentation.
- Performance: visual texture is CSS-only and subtle; media remains the primary decoration.

## Color tokens

| Role | Light | Dark | CSS token |
| --- | --- | --- | --- |
| Page background | `#F7F3EA` | `#1E1B18` | `--color-bg` |
| Surface | `#FFFCF6` | `#29241F` | `--color-surface` |
| Raised surface | `#FFFFFF` | `#342E28` | `--color-surface-raised` |
| Primary text | `#2F2A24` | `#F6EFE5` | `--color-text` |
| Muted text | `#6F665C` | `#C4B9AC` | `--color-text-muted` |
| Primary | `#9A452F` | `#E58A70` | `--color-primary` |
| Primary hover | `#7E3524` | `#F1A087` | `--color-primary-hover` |
| Secondary | `#3F6F6A` | `#78AAA4` | `--color-secondary` |
| Highlight | `#C3923E` | `#DDB566` | `--color-highlight` |
| Border | `#DED5C7` | `#4A423A` | `--color-border` |
| Success | `#39734F` | `#73B98C` | `--color-success` |
| Danger | `#A13D3D` | `#E17C7C` | `--color-danger` |

Do not use color as the only state indicator. All state colors require text, icon, or shape support.

## Typography

- Display and editorial headings: `"Noto Serif SC", "Songti SC", STSong, serif`.
- UI, body, chat and controls: `"Noto Sans SC", "Microsoft YaHei", system-ui, sans-serif`.
- Do not depend on Google Fonts in production; Chinese network availability and font payload are concerns.
- Body: 16px, line-height 1.65.
- Long-form content: 17px desktop, 16px mobile, maximum line width 68 Chinese characters.
- UI labels: 14px or 15px; never below 12px.
- Page title: `clamp(28px, 3vw, 42px)` using the serif stack.

## Spacing and geometry

- Base unit: 4px; common spacing: 8, 12, 16, 24, 32, 48px.
- Control radius: 10px; card radius: 14px; media radius: 12px.
- Touch targets: at least 44×44px, with at least 8px between adjacent targets.
- Content max width: 1440px; feed text max width: 720px.

## Elevation

Use borders before shadows. Shadows remain warm and restrained.

```css
--shadow-sm: 0 1px 2px rgba(55, 45, 35, 0.06);
--shadow-md: 0 8px 24px rgba(55, 45, 35, 0.09);
--shadow-overlay: 0 20px 56px rgba(30, 25, 20, 0.18);
```

## Navigation shell

- Desktop ≥1280px: 224px left navigation, flexible main column, optional 280px right rail.
- Tablet 768–1279px: 80px compact left rail; hide the right rail.
- Mobile <768px: compact top bar plus five-item fixed bottom navigation.
- Bottom navigation: Home, Wall, Create, Forum, Messages.
- Guestbook, Timeline, Profile, Settings and Admin live in the profile/more sheet on mobile.

## Component rules

- Buttons preserve width while loading and disable repeated submission.
- Cards use a surface background and border; only clickable cards get pointer and hover feedback.
- Media reserves its aspect ratio before loading and lazy-loads offscreen.
- Photo wall enhancement must preserve DOM reading order.
- Forms use persistent labels and field-level validation; placeholders are examples only.
- Desktop uses centered dialogs; mobile uses bottom sheets or full-screen editors.
- Modal focus is trapped and restored to its trigger when closed.

## Motion and icons

- Micro-interactions: 150–220ms; page transitions: 220–300ms.
- Animate opacity and transform only; respect `prefers-reduced-motion`.
- Use one SVG icon family, preferably Lucide; never use emoji as UI icons.
- Icon-only controls require accessible labels and desktop tooltips.

## Accessibility baseline

- WCAG AA contrast; normal text ratio at least 4.5:1.
- Visible `:focus-visible` rings on every interactive element.
- Keyboard order follows visual order; provide “skip to main content”.
- Preserve native browser back behavior and sequential heading hierarchy.
- Test at 375, 768, 1024, 1280 and 1440px with no horizontal page scroll.

## Forbidden patterns

- Futuristic/Web3 typography or neon palettes.
- Heavy skeuomorphism, torn paper, fake tape, or random card rotation used repeatedly.
- Glassmorphism that reduces contrast.
- Infinite carousels for primary navigation.
- Hover-only actions without tap and keyboard equivalents.
- Full-resolution media in feeds.
- More than five persistent mobile bottom-navigation destinations.
- Admin controls mixed into normal member pages.

