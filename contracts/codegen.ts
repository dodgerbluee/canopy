import type { CodegenConfig } from '@graphql-codegen/cli';

const config: CodegenConfig = {
  schema: 'graphql/*.graphql',
  documents: ['operations/*.graphql'],
  generates: {
    'generated/graphql.ts': {
      plugins: ['typescript-operations', 'typed-document-node'],
      config: {
        scalars: {
          DateTime: 'string',
          LocalDate: 'string',
          Decimal: 'string',
          MinorUnits: 'string',
        },
        strictScalars: true,
        namingConvention: 'keep',
      },
    },
  },
};

export default config;
