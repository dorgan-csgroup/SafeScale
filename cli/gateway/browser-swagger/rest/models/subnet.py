# coding: utf-8

from __future__ import absolute_import
from datetime import date, datetime  # noqa: F401

from typing import List, Dict  # noqa: F401

from rest.models.base_model_ import Model
from rest.models.subnet_state import SubnetState
from rest.models.virtual_ip import VirtualIp
from rest import util

from rest.models.subnet_state import SubnetState  # noqa: E501
from rest.models.virtual_ip import VirtualIp  # noqa: E501

class Subnet(Model):
    """NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).

    Do not edit the class manually.
    """

    def __init__(self, id=None, name=None, cidr=None, gateway_ids=None, virtual_ip=None, failover=None, state=None, network_id=None):  # noqa: E501
        """Subnet - a model defined in OpenAPI

        :param id: The id of this Subnet.  # noqa: E501
        :type id: str
        :param name: The name of this Subnet.  # noqa: E501
        :type name: str
        :param cidr: The cidr of this Subnet.  # noqa: E501
        :type cidr: str
        :param gateway_ids: The gateway_ids of this Subnet.  # noqa: E501
        :type gateway_ids: List[str]
        :param virtual_ip: The virtual_ip of this Subnet.  # noqa: E501
        :type virtual_ip: VirtualIp
        :param failover: The failover of this Subnet.  # noqa: E501
        :type failover: bool
        :param state: The state of this Subnet.  # noqa: E501
        :type state: SubnetState
        :param network_id: The network_id of this Subnet.  # noqa: E501
        :type network_id: str
        """
        self.openapi_types = {
            'id': str,
            'name': str,
            'cidr': str,
            'gateway_ids': List[str],
            'virtual_ip': VirtualIp,
            'failover': bool,
            'state': SubnetState,
            'network_id': str
        }

        self.attribute_map = {
            'id': 'id',
            'name': 'name',
            'cidr': 'cidr',
            'gateway_ids': 'gatewayIds',
            'virtual_ip': 'virtualIp',
            'failover': 'failover',
            'state': 'state',
            'network_id': 'networkId'
        }

        self._id = id
        self._name = name
        self._cidr = cidr
        self._gateway_ids = gateway_ids
        self._virtual_ip = virtual_ip
        self._failover = failover
        self._state = state
        self._network_id = network_id

    @classmethod
    def from_dict(cls, dikt) -> 'Subnet':
        """Returns the dict as a model

        :param dikt: A dict.
        :type: dict
        :return: The Subnet of this Subnet.  # noqa: E501
        :rtype: Subnet
        """
        return util.deserialize_model(dikt, cls)

    @property
    def id(self):
        """Gets the id of this Subnet.


        :return: The id of this Subnet.
        :rtype: str
        """
        return self._id

    @id.setter
    def id(self, id):
        """Sets the id of this Subnet.


        :param id: The id of this Subnet.
        :type id: str
        """

        self._id = id

    @property
    def name(self):
        """Gets the name of this Subnet.


        :return: The name of this Subnet.
        :rtype: str
        """
        return self._name

    @name.setter
    def name(self, name):
        """Sets the name of this Subnet.


        :param name: The name of this Subnet.
        :type name: str
        """

        self._name = name

    @property
    def cidr(self):
        """Gets the cidr of this Subnet.


        :return: The cidr of this Subnet.
        :rtype: str
        """
        return self._cidr

    @cidr.setter
    def cidr(self, cidr):
        """Sets the cidr of this Subnet.


        :param cidr: The cidr of this Subnet.
        :type cidr: str
        """

        self._cidr = cidr

    @property
    def gateway_ids(self):
        """Gets the gateway_ids of this Subnet.


        :return: The gateway_ids of this Subnet.
        :rtype: List[str]
        """
        return self._gateway_ids

    @gateway_ids.setter
    def gateway_ids(self, gateway_ids):
        """Sets the gateway_ids of this Subnet.


        :param gateway_ids: The gateway_ids of this Subnet.
        :type gateway_ids: List[str]
        """

        self._gateway_ids = gateway_ids

    @property
    def virtual_ip(self):
        """Gets the virtual_ip of this Subnet.


        :return: The virtual_ip of this Subnet.
        :rtype: VirtualIp
        """
        return self._virtual_ip

    @virtual_ip.setter
    def virtual_ip(self, virtual_ip):
        """Sets the virtual_ip of this Subnet.


        :param virtual_ip: The virtual_ip of this Subnet.
        :type virtual_ip: VirtualIp
        """

        self._virtual_ip = virtual_ip

    @property
    def failover(self):
        """Gets the failover of this Subnet.


        :return: The failover of this Subnet.
        :rtype: bool
        """
        return self._failover

    @failover.setter
    def failover(self, failover):
        """Sets the failover of this Subnet.


        :param failover: The failover of this Subnet.
        :type failover: bool
        """

        self._failover = failover

    @property
    def state(self):
        """Gets the state of this Subnet.


        :return: The state of this Subnet.
        :rtype: SubnetState
        """
        return self._state

    @state.setter
    def state(self, state):
        """Sets the state of this Subnet.


        :param state: The state of this Subnet.
        :type state: SubnetState
        """

        self._state = state

    @property
    def network_id(self):
        """Gets the network_id of this Subnet.


        :return: The network_id of this Subnet.
        :rtype: str
        """
        return self._network_id

    @network_id.setter
    def network_id(self, network_id):
        """Sets the network_id of this Subnet.


        :param network_id: The network_id of this Subnet.
        :type network_id: str
        """

        self._network_id = network_id