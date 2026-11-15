// A spec file that cannot load, as a broken install leaves one: its module is not there.
import { test } from '@playwright/test';
import 'tests-playwright-reporter-no-such-module';

test('never registered', async () => undefined);
