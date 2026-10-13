# Cineva Contributor Licence Agreement

Version 1.0

This agreement sets out the terms on which you contribute to Cineva. It
exists so that Cineva can be maintained, defended and, if it ever needs to
be, relicensed — while you keep the copyright in your work and while
Cineva, and your contribution to it, stay open source.

Read it before your first contribution. To agree, follow "How to agree" at
the end.

## 1. Definitions

- **"You"** means the person, or the organisation on whose behalf they act,
  who submits a Contribution and agrees to this agreement.
- **"Maintainer"** means Gary Bowers, and any person or organisation to whom
  stewardship of the Project is later transferred.
- **"Project"** means the Cineva software and its documentation, as published
  in its source repository.
- **"Contribution"** means any work of authorship — code, documentation,
  designs, translations or other material — that You submit to the Project
  for inclusion in it, by a pull or merge request, a patch, a commit, or any
  other means. Something You mark plainly in writing as "not a contribution"
  is not one.

## 2. You keep your copyright

You keep all copyright and other rights in Your Contributions. Nothing in
this agreement stops You using Your Contributions in any way You choose.

## 3. Copyright licence

You grant the Maintainer, and every recipient of software the Maintainer
distributes, a perpetual, worldwide, non-exclusive, no-charge, royalty-free,
irrevocable licence to reproduce, adapt, publicly display, publicly perform,
sublicense and distribute Your Contributions and works derived from them.

## 4. Open-source licensing only

The Maintainer may license Your Contributions to others, as part of the
Project or otherwise, only under:

- the GNU Affero General Public Licence, version 3 or any later version; or
- another licence that, at the time it is applied, is approved as an open
  source licence by the Open Source Initiative or as a free software licence
  by the Free Software Foundation.

This limit binds the Maintainer and anyone to whom the Maintainer's rights
under this agreement pass. It means Your Contributions can be relicensed —
for instance from the AGPL to the GPL or to a permissive licence — but never
under a proprietary licence.

## 5. Patent licence

You grant the Maintainer, and every recipient of software the Maintainer
distributes, a perpetual, worldwide, non-exclusive, no-charge, royalty-free,
irrevocable (except as stated in this section) patent licence to make, have
made, use, sell, offer for sale, import and otherwise transfer Your
Contributions, alone or as combined with the Project. It covers only the
patent claims You can license that are necessarily infringed by Your
Contribution, alone or in that combination.

If anyone starts patent litigation — including a cross-claim or
counterclaim — alleging that the Project, or a Contribution in it, infringes
a patent, every patent licence granted to them under this agreement ends on
the date that litigation is filed.

## 6. What You confirm

By agreeing, You confirm that:

1. You have the legal right to grant the licences above for each
   Contribution You submit.
2. If Your employer, or anyone else, has rights in what You create — through
   an employment contract, for example — You have their permission to
   contribute on these terms, or they have waived those rights.
3. Each Contribution is Your original work, except for any part You identify
   in the Contribution as coming from someone else, together with its source
   and its licence. You will not knowingly submit anything whose licence
   conflicts with this agreement or with the Project's licence.
4. You will tell the Maintainer if You learn that any of these statements
   has become untrue.

## 7. No warranty, no obligation

You provide Your Contributions as they are, without warranty or conditions
of any kind, and You are not expected to support them, except as You choose
to or as the law requires.

The Maintainer is not obliged to accept or use any Contribution.

## 8. Moral rights

Where the law allows, You agree not to assert any moral rights in Your
Contributions in a way that would prevent their use under this agreement —
for instance by objecting to their being changed, combined with other work,
or published without naming You. Contributors are credited in the Project's
history all the same.

## 9. Duration

This agreement covers every Contribution You submit while it is in effect.
You may stop it applying to future Contributions by telling the Maintainer
in writing; the licences already granted for earlier Contributions continue.

A new version of this agreement applies only to Contributions You submit
after You agree to that version.

## How to agree

1. In your first contribution, add a line to [CLA-SIGNATURES.txt](CLA-SIGNATURES.txt):

   ```
   Your Name <you@example.com> | 2026-10-13 | CLA 1.0
   ```

   Use your real name and the email address your commits are made with. If
   you contribute on behalf of an organisation, add its name after yours:
   `Your Name (Example Ltd) <you@example.com> | ...`.

2. Sign off every commit (`git commit -s`). The `Signed-off-by:` line it
   adds states that you submit the commit under this agreement.

The commit that adds your line is the record of your agreement. The check
`scripts/check-cla.sh` confirms that every commit in a change is signed off
by an author listed in CLA-SIGNATURES.txt.
