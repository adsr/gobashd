%global debug_package %{nil}
%global commit cf6e33cddedd243262d1ba5779def0b08820f4d5
%global shortcommit %(c=%{commit}; echo ${c:0:7})

Name:       gobashd
Version:    1.5
Release:    1.%{shortcommit}%{?dist}
Summary:    Asynchronous queryable bash executor
Group:      System Environment/Daemons

License:    PHP
URL:        https://github.com/adsr/%{name}
Source0:    https://github.com/adsr/%{name}/archive/%{commit}.zip
BuildRoot:  %{_tmppath}/%{name}-%{version}-%{release}-root-%(%{__id_u} -n)

BuildRequires: golang

%description
gobashd executes long-running bash scripts asynchronously on behalf of
network-connected clients.

%prep
%setup -q -n %{name}-%{commit}

%build
make %{?_smp_mflags}

%check
make check

%install
rm -rf %{buildroot}
make install DESTDIR=%{buildroot} INSTALL="%{__install} -p" bindir=%{_bindir}
install -v -d -m 755 %{buildroot}%{_localstatedir}/log/%{name}
install -v -d -m 755 %{buildroot}%{_localstatedir}/run/%{name}
install -v -d -m 755 %{buildroot}%{_sysconfdir}/%{name}.d
install -v -p -D -m 755 gobashd.init %{buildroot}%{_initrddir}/%{name}
cp -va scripts/* %{buildroot}%{_sysconfdir}/%{name}.d/

%files
%{_bindir}/*
%{_initrddir}/%{name}
%{_localstatedir}/log/*
%{_localstatedir}/run/*
%{_sysconfdir}/%{name}.d/*

%changelog
* Wed Mar 11 2015 Adam Saponara <as@php.net> 0.2-1
- Initial RPM release
