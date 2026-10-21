import type { CSSProperties } from 'react';
import { useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import {
  formatPrice,
  formatTokens,
  groupQuery,
  PRICE_FIELDS,
  type PriceField,
  type PriceRow,
  parsePrice,
  priceFields,
  priceRows,
  rangeOf,
  unpricedModels,
  utcOffsetOf,
  withOverride,
  withoutOverride,
} from '../../lib/usage.ts';
import { useUsage, useUsagePrices } from '../../lib/use-usage.ts';
import type { UsageFixture } from './usage.tsx';

const FIELD_LABEL: Record<PriceField, () => string> = {
  input: () => t('Input'),
  output: () => t('Output'),
  cacheWrite: () => t('Cache write'),
  cacheRead: () => t('Cache read'),
};

type Editing = { model: string; isNew: boolean; fields: Record<PriceField, string> };

function Editor(props: {
  editing: Editing;
  saving: boolean;
  onChange: (e: Editing) => void;
  onCancel: () => void;
  onSave: (model: string, fields: Record<PriceField, string>) => void;
}) {
  const { editing, saving } = props;
  const price = parsePrice(editing.fields);
  const model = editing.model.trim();
  const valid = price !== null && model !== '' && model.length <= 256;
  return (
    <div className="prow-edit" data-testid="settings.prices.editor">
      <div className="hd2">
        {editing.isNew ? (
          <TextField
            label={t('Model')}
            showLabel
            mono
            value={editing.model}
            placeholder="claude-sonnet-4-5"
            onChange={(e) => props.onChange({ ...editing, model: e.target.value })}
            data-testid="settings.prices.model"
          />
        ) : (
          <b>{editing.model}</b>
        )}
        <span className="rmeta">
          {editing.isNew
            ? t('Not in the built-in table. Your price is kept on the host.')
            : t('US dollars per 1M tokens')}
        </span>
      </div>
      <div className="f4">
        {PRICE_FIELDS.map((f) => (
          <div key={f} className="field-group">
            <label className="field-label" htmlFor={`price-${f}`}>
              {FIELD_LABEL[f]()}
            </label>
            <span className={`th-in${price === null && editing.fields[f] !== '' ? ' bad' : ''}`}>
              <span className="px" aria-hidden="true">
                $
              </span>
              <input
                id={`price-${f}`}
                className="field bare mono"
                inputMode="decimal"
                value={editing.fields[f]}
                onChange={(e) =>
                  props.onChange({ ...editing, fields: { ...editing.fields, [f]: e.target.value } })
                }
                data-testid={`settings.prices.field.${f}`}
              />
            </span>
          </div>
        ))}
      </div>
      <div className="fo">
        <Button onClick={props.onCancel}>{t('Cancel')}</Button>
        <Button
          variant="primary"
          disabled={!valid || saving}
          onClick={() => props.onSave(model, editing.fields)}
          data-testid="settings.prices.save"
        >
          {t('Save')}
        </Button>
      </div>
    </div>
  );
}

function Cell(props: { value: number | undefined; dim?: boolean }) {
  return (
    <span className={`n${props.dim ? ' dim' : ''}`}>
      {props.value === undefined ? '—' : formatPrice(props.value)}
    </span>
  );
}

/** Settings > Usage > Prices: the table behind the estimate, the host's own prices and the tokens-only switch. */
export function PricesTab(props: {
  fixture: UsageFixture | undefined;
  tokensOnly: boolean;
  onTokensOnly: (on: boolean) => void;
}) {
  const { fixture, tokensOnly } = props;
  const prices = useUsagePrices(fixture?.prices);
  const models = useUsage(
    groupQuery(rangeOf('month', fixture?.now ?? Date.now(), utcOffsetOf()), 'model', utcOffsetOf()),
    fixture?.groups.model,
  );
  const [editing, setEditing] = useState<Editing | null>(() => {
    const model = fixture?.editing;
    if (!model || !fixture) return null;
    return {
      model,
      isNew: false,
      fields: priceFields(
        fixture.prices.overrides[model] ?? fixture.prices.defaults[model] ?? null,
      ),
    };
  });

  const data = prices.data;
  const missing = models.data ? unpricedModels(models.data.rows) : [];
  const rows: PriceRow[] = data
    ? priceRows(
        data,
        missing.map((m) => ({ model: m.model, tokens: m.tokens })),
      )
    : [];

  const save = async (model: string, fields: Record<PriceField, string>) => {
    const price = parsePrice(fields);
    if (!data || !price) return;
    if (await prices.save(withOverride(data.overrides, model, price))) setEditing(null);
  };
  const reset = async (model: string) => {
    if (data) await prices.save(withoutOverride(data.overrides, model));
  };
  const style = { '--cols': 'minmax(0,1fr) 60px 60px 68px 68px 104px' } as CSSProperties;

  return (
    <>
      <div className="plist">
        <div className="pr hint">
          <div className="nm">
            <Switch
              label={t('Show tokens only')}
              checked={tokensOnly}
              onChange={props.onTokensOnly}
              data-testid="settings.prices.tokens-only"
            />
            <span className="set-meta">
              {t('Hide estimated cost everywhere, for plans that are not billed per token.')}
            </span>
          </div>
        </div>
      </div>
      <div className="sgrp">
        <span className="lb">{t('Price table')}</span>
      </div>
      <p className="rmeta">
        {tokensOnly
          ? t(
              'Estimated cost is hidden. The prices stay and are used again when you turn this off.',
            )
          : t(
              'US dollars per 1M tokens. Built-in prices ship with Hoplet; a price you set wins over them.',
            )}
      </p>
      {prices.error ? (
        <p className="rmeta" role="alert" data-testid="settings.prices.error">
          {prices.error}
        </p>
      ) : null}
      {data === null ? (
        prices.error ? null : (
          <p className="rmeta" role="status">
            {t('Loading usage…')}
          </p>
        )
      ) : (
        <div className="mp-list pt" data-testid="settings.prices.table">
          <div className="ur hd" style={style}>
            <span>{t('Model')}</span>
            {PRICE_FIELDS.map((f) => (
              <span key={f} className="n">
                {FIELD_LABEL[f]()}
              </span>
            ))}
            <span />
          </div>
          {rows.map((r) => (
            <div key={r.model}>
              <div className="ur" style={style} data-testid="settings.prices.row">
                <span className="un">
                  <span className="t1 mono">{r.model}</span>
                  {r.source === 'none' ? (
                    <span className="t2">
                      {t('{n} tokens without a price').replace('{n}', formatTokens(r.tokens))}
                    </span>
                  ) : null}
                </span>
                {PRICE_FIELDS.map((f) => (
                  <Cell key={f} value={r.price?.[f]} dim={r.price === null} />
                ))}
                <span className="own">
                  {r.source === 'none' ? (
                    <Button
                      variant="quiet"
                      onClick={() =>
                        setEditing({ model: r.model, isNew: false, fields: priceFields(null) })
                      }
                    >
                      {t('Add price')}
                    </Button>
                  ) : (
                    <>
                      <span className="tg">
                        {r.source === 'yours' ? t('Yours') : t('Built in')}
                      </span>
                      <IconButton
                        label={t('Edit price of {model}').replace('{model}', r.model)}
                        icon={Icon.pen()}
                        onClick={() =>
                          setEditing({ model: r.model, isNew: false, fields: priceFields(r.price) })
                        }
                      />
                      {r.source === 'yours' ? (
                        <IconButton
                          label={t('Reset to the built-in price')}
                          icon={Icon.retry()}
                          onClick={() => reset(r.model)}
                        />
                      ) : null}
                    </>
                  )}
                </span>
              </div>
              {editing && !editing.isNew && editing.model === r.model ? (
                <Editor
                  editing={editing}
                  saving={prices.saving}
                  onChange={setEditing}
                  onCancel={() => setEditing(null)}
                  onSave={save}
                />
              ) : null}
            </div>
          ))}
          {editing?.isNew ? (
            <Editor
              editing={editing}
              saving={prices.saving}
              onChange={setEditing}
              onCancel={() => setEditing(null)}
              onSave={save}
            />
          ) : null}
        </div>
      )}
      {data && !editing?.isNew ? (
        <div className="padd">
          <Button
            icon={Icon.plus()}
            onClick={() => setEditing({ model: '', isNew: true, fields: priceFields(null) })}
            data-testid="settings.prices.add"
          >
            {t('Add a model price')}
          </Button>
        </div>
      ) : null}
    </>
  );
}
