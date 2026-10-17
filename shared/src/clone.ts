import * as v from 'valibot';

/** Why a target cannot be cloned into, as the check answers it (docs/api.md#get-apidevicesdeviceidclone). */
export const CloneCheckReasonSchema = v.picklist([
  'ok',
  'no-origin',
  'no-access',
  'destination-taken',
]);
export type CloneCheckReason = v.InferOutput<typeof CloneCheckReasonSchema>;

/** Why a destination is taken. */
export const CloneDestinationDetailSchema = v.picklist([
  'exists',
  'outside-root',
  'symlink',
  'not-writable',
]);
export type CloneDestinationDetail = v.InferOutput<typeof CloneDestinationDetailSchema>;

/** Why a clone failed; `cancelled` answers a `device.clone.cancel`. */
export const CloneFailureReasonSchema = v.picklist([
  'no-access',
  'no-space',
  'destination-taken',
  'timeout',
  'git-failed',
  'cancelled',
]);
export type CloneFailureReason = v.InferOutput<typeof CloneFailureReasonSchema>;

/** The longest `detail` an agent sends for a failed clone: git's text after credentials are removed. */
export const CLONE_DETAIL_MAX_BYTES = 2048;
