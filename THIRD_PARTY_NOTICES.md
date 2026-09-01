# Third-party notices

The Herdr Web source and original project files are covered by the root [MIT license](LICENSE). The components listed here are separate works and retain their own copyrights and license terms. Nothing in the root license relicenses these components.

This inventory covers the direct runtime and browser dependencies in `go.mod` and `package.json` and the runtime-transitive Go modules linked into the executable. Versions are the locked versions used to produce the release. Release archives include exact upstream license and notice files for every linked module plus the Go runtime.

## Browser dependencies

### `@xterm/xterm` 6.0.0 — MIT

Used by the embedded terminal UI. Copyright notices and license text from the package are reproduced here.

```text
Copyright (c) 2017-2019, The xterm.js authors (https://github.com/xtermjs/xterm.js)
Copyright (c) 2014-2016, SourceLair Private Company (https://www.sourcelair.com)
Copyright (c) 2012-2013, Christopher Jeffrey (https://github.com/chjj/)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

### `@xterm/addon-fit` 0.11.0 — MIT

```text
Copyright (c) 2019, The xterm.js authors (https://github.com/xtermjs/xterm.js)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

### Cascadia Mono Nerd Font 3.5.1 — OFL-1.1, MIT, and icon-set terms

The embedded terminal bundles the unchanged `CaskaydiaMonoNerdFontMono-Regular.ttf` from the official Nerd Fonts 3.5.1 `CascadiaMono.tar.xz` release. Its SHA-256 is `0bc1e80eb7d1c0a1debb433a21da6e686b15556e1d54fcfe47f87f7379276830`.

The Cascadia Mono base font remains under the SIL Open Font License 1.1, including Microsoft's Reserved Font Name. The Nerd Fonts project additions remain under the MIT license. The incorporated symbol sets retain the individual terms and attributions listed in the exact upstream notice. Source copies are tracked as [`LICENSE-CASCADIA-MONO.txt`](web/fonts/LICENSE-CASCADIA-MONO.txt), [`LICENSE-NERD-FONTS.txt`](web/fonts/LICENSE-NERD-FONTS.txt), and [`NOTICE-NERD-FONTS.txt`](web/fonts/NOTICE-NERD-FONTS.txt); release archives reproduce all three under `LICENSES/`, and the browser bundle serves the same files beside its assets.

### `esbuild` 0.28.2 — MIT (build-time)

`esbuild` is used to produce the browser bundle and is not a runtime service dependency. Its license is included because it is a direct build dependency.

```text
MIT License

Copyright (c) 2020 Evan Wallace

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
### `@biomejs/biome` 2.5.11 — MIT OR Apache-2.0 (build-time)

Biome is a direct formatting/linting build tool. Its package metadata offers the work under either MIT or Apache-2.0. The package retains two MIT notice files, reproduced separately and verbatim below; the Apache-2.0 text is reproduced in the Go dependency section.

`LICENSE-MIT`:

```text
MIT License

Copyright (c) 2023 Biome Developers and Contributors.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

`ROME-LICENSE-MIT`:

```text
MIT License

Copyright (c) 2020-2023 Rome Tools is Rome Tools, Inc. and its affiliates.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### `knip` 6.33.0 — ISC (build-time)

Knip is a direct dependency-analysis build tool.

```text
ISC License (ISC)

Copyright 2022-2026 Lars Kappert

Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
```

### `playwright` 1.62.1 and `playwright-core` 1.62.1 — Apache-2.0 (test-time)

Playwright drives the Chromium end-to-end verification. These packages and downloaded browser binaries are test-time dependencies; they are not shipped in the Herdr Web release archive. The Apache-2.0 text below applies to both packages.

The packages contain the following identical `NOTICE`:

```text
Playwright
Copyright (c) Microsoft Corporation

This software contains code derived from the Puppeteer project (https://github.com/puppeteer/puppeteer),
available under the Apache 2.0 license (https://github.com/puppeteer/puppeteer/blob/master/LICENSE).
```

## Go dependencies

The following modules are direct dependencies of the executable. The binary also links the listed runtime-transitive modules. Test-only modules are not part of the release executable. Every release archive reproduces each module's exact upstream `LICENSE` and `NOTICE` files under `LICENSES/`; it also includes the building Go toolchain's `LICENSE` and `PATENTS` files with the toolchain version in their filenames.

| Module | Version | Role | License |
| --- | --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | v3.20.0 | OIDC discovery and ID-token verification | Apache License 2.0; upstream CoreOS notice shipped in `LICENSES/` |
| `github.com/creack/pty` | v1.1.24 | PTY allocation and I/O | MIT |
| `github.com/gorilla/websocket` | v1.5.3 | WebSocket transport | BSD 2-Clause |
| `golang.org/x/term` | v0.45.0 | Terminal support | BSD 3-Clause |
| `github.com/go-jose/go-jose/v4` | v4.1.4 | Runtime-transitive JOSE implementation | Apache License 2.0 |
| `golang.org/x/oauth2` | v0.36.0 | Direct OIDC HTTP dependency | BSD 3-Clause |
| `golang.org/x/sys` | v0.47.0 | Runtime-transitive platform support | BSD 3-Clause |

### Apache License 2.0

This license applies to `github.com/coreos/go-oidc/v3` v3.20.0, `github.com/go-jose/go-jose/v4` v4.1.4, `@biomejs/biome` 2.5.11 (one of its offered licenses), and `playwright`/`playwright-core` 1.62.1.

```text
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but not
      limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work of authorship, whether in
      Source or Object form, that is based on (or derived from) the Work
      and for which the editorial revisions, annotations, elaborations,
      or other modifications represent, as a whole, an original work of
      authorship. For the purposes of this definition, Derivative Works
      shall not include works that remain separable from, or merely link
      (or bind by name) to the interfaces of, the Work and Derivative Works
      thereof.

      "Contribution" shall mean any work of authorship, including the
      original version of the Work and any modifications or additions to
      that Work or Derivative Works thereof, that is intentionally submitted
      to Licensor for inclusion in the Work by the copyright owner or by an
      individual or Legal Entity authorized to submit on behalf of the
      copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication sent on electronic mailing lists, source code control
      systems, and issue tracking systems that are managed by, or on behalf
      of, the Licensor for the purpose of discussing and improving the Work,
      but excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable by
      such Contributor that are necessarily infringed by their Contribution(s)
      alone or by combination of their Contribution(s) with the Work to which
      such Contribution(s) was submitted. If You institute patent litigation
      against any entity (including a cross-claim or counterclaim in a
      lawsuit) alleging that the Work or a Contribution incorporated within
      the Work constitutes direct or contributory patent infringement, then
      any patent licenses granted to You under this License for that Work
      shall terminate as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the Work
      or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You meet
      the following conditions:

      (a) You must give any other recipients of the Work or Derivative Works
          a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works that
          You distribute, all copyright, patent, trademark, and attribution
          notices from the Source form of the Work, excluding those notices
          that do not pertain to any part of the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one of the
          following places: within a NOTICE text file distributed as part of
          the Derivative Works; within the Source form or documentation, if
          provided along with the Derivative Works; or, within a display
          generated by the Derivative Works, if and wherever such third-party
          notices normally appear. The contents of the NOTICE file are for
          informational purposes only and do not modify the License. You may
          add Your own attribution notices within Derivative Works that You
          distribute, alongside or as an addendum to the NOTICE text from
          the Work, provided that such additional attribution notices cannot
          be construed as modifying the License.

      You may add Your own copyright statement to Your modifications and may
      provide additional or different license terms and conditions for use,
      reproduction, or distribution of Your modifications, or for any such
      Derivative Works as a whole, provided Your use, reproduction, and
      distribution of the Work otherwise complies with the conditions stated
      in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work by
      You to the Licensor shall be under the terms and conditions of this
      License, without any additional terms or conditions. Notwithstanding
      the above, nothing herein shall supersede or modify the terms of any
      separate license agreement you may have executed with Licensor
      regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or agreed to
      in writing, Licensor provides the Work (and each Contributor provides
      its Contributions) on an "AS IS" BASIS, WITHOUT WARRANTIES OR
      CONDITIONS OF ANY KIND, either express or implied, including, without
      limitation, any warranties or conditions of TITLE, NON-INFRINGEMENT,
      MERCHANTABILITY, or FITNESS FOR A PARTICULAR PURPOSE. You are solely
      responsible for determining the appropriateness of using or
      redistributing the Work and assume any risks associated with Your
      exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise, unless
      required by applicable law (such as deliberate and grossly negligent
      acts) or agreed to in writing, shall any Contributor be liable to You
      for damages, including any direct, indirect, special, incidental, or
      consequential damages of any character arising as a result of this
      License or out of the use or inability to use the Work (including but
      not limited to damages for loss of goodwill, work stoppage, computer
      failure or malfunction, or any and all other commercial damages or
      losses), even if such Contributor has been advised of the possibility
      of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing the
      Work or Derivative Works thereof, You may choose to offer, and charge
      a fee for, acceptance of support, warranty, indemnity, or other
      liability obligations and/or rights consistent with this License.
      However, in accepting such obligations, You may act only on Your own
      behalf and on Your sole responsibility, not on behalf of any other
      Contributor, and only if You agree to indemnify, defend, and hold each
      Contributor harmless for any liability incurred by, or claims asserted
      against, such Contributor by reason of your accepting any such warranty
      or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "{}"
      replaced with your own identifying information. (Don't include the
      brackets!) The text should be enclosed in the appropriate comment
      syntax for the file format. We also recommend that a file or class
      name and description of purpose be included on the same "printed page"
      as the copyright notice for easier identification within third-party
      archives.

   Copyright {yyyy} {name of copyright owner}

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

## Keeping this file current

When a direct dependency or bundled asset changes, update this inventory from the exact locked version, preserve all upstream copyright notices, and keep the corresponding files in release archives. Distinguish build-time tools from code linked into the runtime. Do not replace a third-party license with the root MIT license.
