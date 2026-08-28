#!/bin/sh
set -e

create_db_and_user() {
	username="$1"
	dbname="$2"
	password="${3:-$username}"

	if [ -z "$username" ] || [ -z "$dbname" ]; then
		echo "usage: create_db_and_user <username> <dbname> [password]" >&2
		return 1
	fi

	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
		DO \$\$
		BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$username') THEN
				CREATE ROLE "$username" LOGIN PASSWORD '$password';
			END IF;
		END
		\$\$;

		SELECT 'CREATE DATABASE "$dbname" OWNER "$username"'
		WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$dbname')\gexec

		GRANT ALL PRIVILEGES ON DATABASE "$dbname" TO "$username";
	EOSQL

	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$dbname" <<-EOSQL
		GRANT ALL ON SCHEMA public TO "$username";
	EOSQL
}

create_db_and_user control control control
