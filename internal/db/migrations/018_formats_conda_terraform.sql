-- Migration 018: add conda and terraform to repositories format check constraint
-- PostgreSQL does not support ALTER CONSTRAINT — drop and recreate.

ALTER TABLE repositories
    DROP CONSTRAINT IF EXISTS repositories_format_check;

ALTER TABLE repositories
    ADD CONSTRAINT repositories_format_check CHECK (format IN (
        'maven2','npm','docker','pypi','go','nuget','helm','raw',
        'apt','yum','cargo','conan','conda','terraform'
    ));
