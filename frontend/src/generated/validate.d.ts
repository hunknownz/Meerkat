/* Generated from contracts/workflow.schema.json by scripts/generate-types.mjs. Do not edit. */
export interface Validator { (data: unknown): boolean; errors?: { instancePath: string; message?: string }[] | null }
export declare const validateEnvelope: Validator;
export declare const validateSettingsInput: Validator;
