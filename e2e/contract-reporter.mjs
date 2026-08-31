const expectedByProject = {
  'chromium-desktop': [
    '@desktop serves the exact embedded production bundle and bridges PTY/completion state',
    '@desktop a crashed Herdr client reports its exit and can start a fresh attachment',
    '@desktop closing the attachment cancels and reaps only its target child',
  ],
  'chromium-mobile': [
    '@mobile Type uses the native textarea and controls never refocus the terminal',
  ],
};

function sorted(values) {
  return [...values].sort((left, right) => left.localeCompare(right));
}

export default class ContractReporter {
  onBegin(_config, suite) {
    const project = process.env.HERDR_WEB_CLIENT_EXPECTED_PROJECT;
    const expected = project
      ? expectedByProject[project]
      : Object.values(expectedByProject).flat();
    if (!expected) {
      this.failure = `unknown expected browser project: ${project}`;
      return;
    }

    this.expected = expected;
    this.statuses = new Map();
    const actual = suite.allTests().map((testCase) => testCase.title);
    if (JSON.stringify(sorted(actual)) !== JSON.stringify(sorted(expected))) {
      this.failure = [
        `browser contract inventory mismatch${project ? ` for ${project}` : ''}`,
        `expected: ${JSON.stringify(sorted(expected))}`,
        `actual: ${JSON.stringify(sorted(actual))}`,
      ].join('\n');
    }
  }

  onTestEnd(testCase, result) {
    if (this.expected?.includes(testCase.title)) {
      this.statuses.set(testCase.title, result.status);
    }
  }

  onEnd() {
    if (!this.failure) {
      const incomplete = this.expected.filter(
        (title) => this.statuses.get(title) !== 'passed',
      );
      if (incomplete.length > 0) {
        this.failure = `browser contracts did not pass: ${JSON.stringify(
          incomplete.map((title) => ({
            title,
            status: this.statuses.get(title) ?? 'not run',
          })),
        )}`;
      }
    }
    if (!this.failure) return;
    console.error(this.failure);
    return { status: 'failed' };
  }
}
