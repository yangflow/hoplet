import { useState } from 'react';
import { t } from '../lib/i18n.ts';
import { Button } from './Button.tsx';
import { TextField } from './TextField.tsx';

/** First-run gate: the web UI holds no data of its own, the token unlocks the API. */
export function TokenGate(props: { onSave: (t: string) => void }) {
  const [value, setValue] = useState('');
  return (
    <div className="gate">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (value.trim()) props.onSave(value.trim());
        }}
      >
        <h1>hoplet</h1>
        <p>{t('Enter the server access token (HOPLET_TOKEN of the core)')}</p>
        <TextField
          label={t('Access token')}
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={t('Access token')}
          data-testid="gate.input"
        />
        <Button type="submit" variant="primary" data-testid="gate.submit">
          {t('Enter')}
        </Button>
      </form>
    </div>
  );
}
